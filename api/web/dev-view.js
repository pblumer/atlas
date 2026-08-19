// The Developer View (ADR-0145): a full-screen modal for editing a code-bearing
// field, opened with F2 from the field itself.
//
// The Modeler's property panel is a form. A form is the right shape for a name, a
// due date or a retry budget, and the wrong shape for the fields that hold *code* —
// a FEEL expression, a PowerShell/Python/JavaScript job script, a JSON value, a
// Markdown documentation text, an HTML fragment. Those are edited in a 3-row box
// with no room for the two things the author actually needs beside the code: which
// variables are in scope, and what the language's functions do.
//
// F2 lifts exactly that field into a modal that has room for both. The code itself
// is the same code-editor.js surface as inline (same highlighting, completion,
// validation, drag-and-drop targets — nothing new to learn), so the modal only adds
// what the panel has no space for:
//
//   Variables  the in-scope names grouped by where they come from — the activity's
//              own input mappings, what it writes back, the process scope, linked
//              form fields, data objects. Click to insert at the caret, or drag.
//              Each carries the value it actually holds in a real instance of this
//              process, so "what shape is this thing?" is answered by the running
//              system rather than guessed from the name.
//   Functions  the language's function catalogue, grouped, with signatures.
//   Help       an overview of the language plus the selected function's page:
//              signature, description, a worked example that can be inserted.
//   Examples   ready-made snippets for the language.
//   Test       the existing FEEL evaluate / script run endpoints, in place.
//
// Nothing here is persistent state: the modal edits a copy and writes it back to the
// field on Apply, dispatching the same input/change events a keystroke would, so the
// panel's existing save wiring runs unchanged and undo/redo behaves as always.
//
// Buildless and self-contained (ADR-0012, ADR-0013).

import { attachCodeEditor } from "./code-editor.js";
import { devLang, snippetsFor } from "./dev-lang.js";

const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

// The scope sections of the Variables pane, in the order they are shown: closest to
// the edited element first, widest last. A variable with an unknown scope falls into
// "Process".
const SCOPES = [
  { key: "input", title: "Input", hint: "Read by this element (its input mappings)" },
  { key: "output", title: "Output", hint: "Written by this element" },
  { key: "process", title: "Process scope", hint: "Available anywhere in the instance" },
  { key: "form", title: "Form fields", hint: "Contributed by a linked form" },
  { key: "data", title: "Data objects", hint: "Per-instance data objects (ADR-0053)" },
];

// Where the modal's layout is remembered between openings: the side panel's
// collapsed/expanded choice, its width, and the modal's own size and position. A
// developer who has arranged the window for their screen should find it that way
// next time. All three degrade to the default when localStorage is unavailable
// (private mode, an embedded frame).
const SIDE_KEY = "atlas.devview.side";
const SIDEW_KEY = "atlas.devview.sidewidth";
const GEOM_KEY = "atlas.devview.geometry";

// Layout floors. The modal may be dragged and resized freely above these; below
// them the panes stop being usable, and a window dragged fully off-screen cannot be
// dragged back, so KEEP_ON_SCREEN of it always stays reachable.
const MIN_SIDE = 200;
const MIN_MODAL_W = 520;
const MIN_MODAL_H = 320;
const KEEP_ON_SCREEN = 120;

let openView = null; // the single live Developer View, if any

// devViewOpen reports whether a Developer View is currently on screen — the F2
// handler uses it so the shortcut inside the modal isn't the shortcut that opened it.
export function devViewOpen() {
  return !!openView;
}

// closeDevView closes the live view, if any, without applying. Exported for hosts
// that tear their page down under it (a route change, a re-render).
export function closeDevView() {
  if (openView) openView.close();
}

// markDevField declares a field as code-bearing: it records the language so F2 can
// find it, gives it an accessible title for the modal header, and — when the field
// has already been upgraded to a code editor — adds a small "open" affordance so the
// shortcut is discoverable without reading a hint line.
// Returns the field, so it composes with the enhance* helpers in editor.js.
export function markDevField(field, lang, opts = {}) {
  if (!field || !devLang(lang)) return field;
  field.dataset.devlang = lang;
  if (opts.title) field.dataset.devtitle = opts.title;
  const wrap = field.closest(".code-editor");
  if (wrap && !wrap.querySelector(".dev-open")) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "dev-open";
    btn.title = "Open the developer view (F2)";
    btn.setAttribute("aria-label", "Open the developer view");
    btn.textContent = "</>";
    // mousedown, not click: the field must not lose the caret before we read it.
    btn.addEventListener("mousedown", (e) => {
      e.preventDefault();
      field.focus();
      field.dispatchEvent(new CustomEvent("dev-view-open", { bubbles: true }));
    });
    wrap.appendChild(btn);
  }
  return field;
}

// installDevShortcut wires F2 (and the "</>" button's dev-view-open event) on a
// root. `resolve(field)` supplies the per-field context — variables, validate,
// evaluate, run — at the moment the view opens, so it always reflects the current
// selection rather than whatever was true when the panel was rendered.
// Idempotent per root.
const resolvers = new WeakMap();

export function installDevShortcut(root = document, resolve = () => ({})) {
  if (!root) return;
  // Re-installing replaces the resolver rather than stacking another listener: the
  // Modeler remounts (a different draft, a different project) and the new mount's
  // modeler must be the one the view reads from.
  resolvers.set(root, resolve);
  if (root.__devShortcut) return;
  root.__devShortcut = true;

  const openFor = (field) => {
    if (!field || devViewOpen()) return;
    let ctx = {};
    try { ctx = (resolvers.get(root) || (() => ({})))(field) || {}; } catch { ctx = {}; }
    openDevView(field, ctx);
  };

  // Capture phase: the code editor's own keydown handler runs on the field, and F2
  // must win over anything the page binds later.
  root.addEventListener("keydown", (e) => {
    if (e.key !== "F2" || e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return;
    if (devViewOpen()) return;
    const el = document.activeElement;
    const field = el && el.closest ? el.closest("[data-devlang]") : null;
    if (!field) return;
    e.preventDefault();
    openFor(field);
  }, true);

  root.addEventListener("dev-view-open", (e) => {
    const field = e.target && e.target.closest ? e.target.closest("[data-devlang]") : null;
    openFor(field);
  });
}

// fieldTitle names the edited field in the modal header: an explicit data-devtitle,
// else the property panel label it sits under, else its accessible name.
function fieldTitle(field) {
  if (field.dataset.devtitle) return field.dataset.devtitle;
  const label = field.closest("label");
  const span = label && label.querySelector("span");
  const text = span && span.textContent.trim();
  if (text) return text;
  return field.getAttribute("aria-label") || field.placeholder || "Value";
}

// insertText is how every pane writes into the editor: splice at the caret, restore
// a sensible selection, then fire `input` so the highlighter, the gutter and the
// validator all update exactly as they would for a keystroke.
function insertText(ta, text, caretOffset) {
  const start = ta.selectionStart ?? ta.value.length;
  const end = ta.selectionEnd ?? start;
  ta.value = ta.value.slice(0, start) + text + ta.value.slice(end);
  const caret = start + (caretOffset === undefined ? text.length : caretOffset);
  ta.focus();
  ta.setSelectionRange(caret, caret);
  ta.dispatchEvent(new Event("input", { bubbles: true }));
}

// varInsert renders a variable reference the way the language reads it: PowerShell
// takes the instance's variables as `$name`, every other language by bare name.
function varInsert(lang, name) {
  return lang === "powershell" ? "$" + name : name;
}

// groupVariables buckets the caller's variables into the SCOPES sections.
function groupVariables(list) {
  const byScope = new Map(SCOPES.map((s) => [s.key, []]));
  for (const v of list || []) {
    const name = typeof v === "string" ? v : v && v.name;
    if (!name) continue;
    const scope = (typeof v === "object" && v.scope) || "process";
    (byScope.get(scope) || byScope.get("process")).push({
      name,
      detail: (typeof v === "object" && v.detail) || "variable",
    });
  }
  return SCOPES
    .map((s) => ({ ...s, items: (byScope.get(s.key) || []).sort((a, b) => a.name.localeCompare(b.name)) }))
    .filter((s) => s.items.length);
}

// sampleText renders a variable's live value for the one line a row can spare:
// JSON and long strings are cut, the full value stays in the row's title.
function sampleText(sample) {
  if (!sample) return "";
  const raw = sample.kind === "string" ? JSON.stringify(sample.value) : String(sample.value ?? "");
  const flat = raw.replace(/\s+/g, " ").trim();
  return flat.length > 60 ? flat.slice(0, 59) + "…" : flat;
}

// sampleJSON turns the instance's typed variable views into the plain JSON object
// the Test panel wants. A value the server could not type stays a string, which is
// what it already is.
function sampleJSON(values) {
  const out = {};
  for (const [name, s] of Object.entries(values || {})) {
    if (!s) continue;
    if (s.kind === "json") { try { out[name] = JSON.parse(s.value); continue; } catch { /* keep the text */ } }
    if (s.kind === "number") { const n = Number(s.value); out[name] = Number.isNaN(n) ? s.value : n; continue; }
    if (s.kind === "boolean") { out[name] = s.value === "true" || s.value === true; continue; }
    out[name] = s.value;
  }
  return out;
}

// instanceLabel names one instance in the picker: its key, whether it is still
// running, and which version of the process it runs.
function instanceLabel(i) {
  return `${i.key}${i.state ? " · " + i.state : ""}${i.version ? " · v" + i.version : ""}`;
}

// matches is the pane filter: a case-insensitive substring over the visible text.
const matches = (q, ...fields) => !q || fields.some((f) => String(f || "").toLowerCase().includes(q));

// openDevView lifts `field` into the modal. Options:
//   lang       language id (defaults to the field's data-devlang)
//   title      header title (defaults to the field's label)
//   variables  [{ name, detail, scope }] — scope is one of SCOPES' keys
//   validate   (src) -> Promise<{ ok, error }>   live validation, as inline
//   evaluate   (src, vars) -> Promise<{ ok, result, error }>   the Test panel (FEEL)
//   run        (src, vars) -> Promise<{ ok, result, error }>   the Test panel (script)
//   samples    ({ force, instanceKey }) -> Promise<{ instance, values, instances }
//              | null>   live values for the variables, read from a real instance of
//              this process; `instances` is what the picker offers, `instanceKey`
//              asks for one of them, `force` bypasses the caller's cache
//   lockPrefix (value) -> protected leading length (the fx '=' marker)
//   onApply    (value) -> void, after the field has been written
// Returns a handle { close, apply, el } or null when the field isn't a code field.
export function openDevView(field, opts = {}) {
  if (!field || openView) return null;
  const langId = opts.lang || field.dataset.devlang;
  const lang = devLang(langId);
  if (!lang) return null;

  const snippets = snippetsFor(lang.id);
  const groups = typeof lang.reference === "function" ? lang.reference() : [];
  const canTest = !!(opts.evaluate || opts.run);
  const original = field.value;
  const restoreFocus = document.activeElement;

  const overlay = document.createElement("div");
  overlay.className = "dev-overlay";
  overlay.innerHTML = `
    <div class="dev-modal" role="dialog" aria-modal="true" aria-label="Developer view">
      <header class="dev-head" title="Drag to move · double-click to reset the layout">
        <span class="dev-badge">${esc(lang.label)}</span>
        <strong class="dev-title">${esc(opts.title || fieldTitle(field))}</strong>
        <span class="dev-dirty" hidden title="Unsaved changes">●</span>
        <span class="dev-head-sp"></span>
        ${canTest ? `<button type="button" class="btn ghost small dev-test-toggle" aria-pressed="false">Test</button>` : ""}
        ${lang.format ? `<button type="button" class="btn ghost small dev-fmt">{ } Format</button>` : ""}
        <button type="button" class="btn ghost small dev-cancel">Cancel</button>
        <button type="button" class="btn small dev-apply">Apply</button>
      </header>
      <div class="dev-body">
        <div class="dev-main">
          <div class="dev-editor"><textarea class="dev-ta" spellcheck="false"></textarea></div>
          ${canTest ? `
          <div class="dev-run" hidden>
            <label class="dev-run-label" for="dev-run-vars">Sample variables (JSON)</label>
            <textarea id="dev-run-vars" class="dev-run-vars" rows="4" spellcheck="false" placeholder='{ "amount": 100 }'></textarea>
            <div class="dev-run-row">
              <button type="button" class="btn neutral small dev-run-btn">Run</button>
              <button type="button" class="btn ghost small dev-run-fill" hidden>From instance</button>
              <span class="dev-run-out" aria-live="polite"></span>
            </div>
            <pre class="dev-run-detail" hidden></pre>
          </div>` : ""}
        </div>
        <div class="dev-split" role="separator" aria-orientation="vertical"
             aria-label="Resize the side panel" tabindex="0" title="Drag to resize · arrow keys also work"></div>
        <aside class="dev-side">
          <div class="dev-tabs" role="tablist">
            <button type="button" class="dev-side-toggle" title="Collapse the panel" aria-label="Collapse the panel" aria-expanded="true">›</button>
            <button type="button" role="tab" class="dev-tab active" data-tab="vars" aria-selected="true">Variables</button>
            <button type="button" role="tab" class="dev-tab" data-tab="fns" aria-selected="false"${groups.length ? "" : " hidden"}>Functions</button>
            <button type="button" role="tab" class="dev-tab" data-tab="help" aria-selected="false">Help</button>
            <button type="button" role="tab" class="dev-tab" data-tab="snips" aria-selected="false"${snippets.length ? "" : " hidden"}>Examples</button>
          </div>
          <input type="search" class="dev-filter" placeholder="Filter…" aria-label="Filter" />
          <div class="dev-pane dev-pane-vars" role="tabpanel"></div>
          <div class="dev-pane dev-pane-fns" role="tabpanel" hidden></div>
          <div class="dev-pane dev-pane-help" role="tabpanel" hidden></div>
          <div class="dev-pane dev-pane-snips" role="tabpanel" hidden></div>
        </aside>
      </div>
      <footer class="dev-foot">
        <span class="dev-pos">Ln 1, Col 1</span>
        <span class="dev-count"></span>
        <span class="dev-confirm" hidden>Discard your changes? <button type="button" class="linklike dev-discard">Discard</button> · <button type="button" class="linklike dev-keep">Keep editing</button></span>
        <span class="dev-foot-sp"></span>
        <span class="dev-keys"><kbd>Ctrl</kbd>+<kbd>Space</kbd> completions · <kbd>Ctrl</kbd>+<kbd>Enter</kbd> apply · <kbd>Esc</kbd> close</span>
      </footer>
    </div>`;
  document.body.appendChild(overlay);

  const q = (sel) => overlay.querySelector(sel);
  const ta = q(".dev-ta");
  ta.value = original;

  // The same editing surface as inline — only bigger, and always with a gutter so a
  // long script's error markers and line numbers line up.
  const editor = attachCodeEditor(ta, {
    lang: lang.module,
    variables: (opts.variables || []).map((v) => (typeof v === "string" ? { name: v, detail: "variable" } : v)),
    validate: opts.validate,
    lockPrefix: opts.lockPrefix,
    wrap: lang.wrap,
    gutter: true,
  });

  // ---------- panes ----------

  const paneVars = q(".dev-pane-vars");
  const paneFns = q(".dev-pane-fns");
  const paneHelp = q(".dev-pane-help");
  const paneSnips = q(".dev-pane-snips");
  const filter = q(".dev-filter");
  let selectedItem = null; // the reference item the Help pane is showing

  // Live sample values. `state` drives the strip at the top of the Variables pane:
  // idle (never asked) → loading → ready (an instance was found) / none (the process
  // has no instance yet, or isn't deployed) / error. Loading is lazy and best-effort:
  // the modal is fully usable before, during and after a failed load.
  let lastFilled = null; // the Test sample we put in the box, so we know it is ours
  let samples = { state: opts.samples ? "loading" : "idle", instance: null, values: {}, instances: [] };

  async function loadSamples(req = {}) {
    if (!opts.samples) return;
    samples = { ...samples, state: "loading" };
    if (tab === "vars") renderVars();
    let got = null;
    try { got = await opts.samples({ force: !!req.force, instanceKey: req.instanceKey }); }
    catch { samples = { state: "error", instance: null, values: {}, instances: [] }; if (tab === "vars") renderVars(); return; }
    if (!document.contains(overlay)) return; // closed while we were waiting
    samples = got && got.instance
      ? { state: "ready", instance: got.instance, values: got.values || {}, instances: got.instances || [got.instance] }
      : { state: "none", instance: null, values: {}, instances: [] };
    if (tab === "vars") renderVars();
    // An empty Test box is filled with the real instance's variables — the sample a
    // developer would otherwise type by hand, only correct. A box the author has
    // already written in is never overwritten behind their back; the "From instance"
    // button is how they ask for that.
    if (canTest && samples.state === "ready") {
      const box = q(".dev-run-vars");
      // Untouched, or still holding exactly what a previous load put there (which is
      // the case right after switching instances) — refill it. Anything the author
      // edited stays theirs.
      if (box && (!box.value.trim() || box.value === lastFilled)) {
        lastFilled = JSON.stringify(sampleJSON(samples.values), null, 2);
        box.value = lastFilled;
      }
      const fill = q(".dev-run-fill");
      if (fill) fill.hidden = false;
    }
  }

  // sampleStripHTML is the Variables pane's header: which instance the values come
  // from, and a way to ask again once the process has run.
  function sampleStripHTML() {
    if (!opts.samples) return "";
    const reload = `<button type="button" class="linklike dev-sample-reload">Reload</button>`;
    if (samples.state === "loading") return `<div class="dev-sample-strip">Reading a live instance…</div>`;
    if (samples.state === "error") return `<div class="dev-sample-strip">Live values unavailable · ${reload}</div>`;
    if (samples.state === "none") {
      return `<div class="dev-sample-strip">No instance to read values from yet — deploy and run the process once · ${reload}</div>`;
    }
    if (samples.state === "ready") {
      const i = samples.instance;
      const list = samples.instances.length ? samples.instances : [i];
      // More than one instance to choose from: the values are a *sample*, and which
      // sample matters — the one that took the branch being written about is not
      // always the newest one.
      const picker = list.length > 1
        ? `<select class="dev-sample-pick" aria-label="Instance to read values from">${
            list.map((r) => `<option value="${esc(String(r.key))}"${r.key === i.key ? " selected" : ""}>${esc(instanceLabel(r))}</option>`).join("")
          }</select>`
        : `<b>${esc(instanceLabel(i))}</b>`;
      const when = i.createdAt ? new Date(i.createdAt).toLocaleString() : "";
      return `<div class="dev-sample-strip ready" title="${esc(when)}">Values from instance ${picker} · ${reload}</div>`;
    }
    return "";
  }

  function renderVars() {
    const query = filter.value.trim().toLowerCase();
    const sections = groupVariables(opts.variables);
    const strip = sampleStripHTML();
    if (!sections.length) {
      paneVars.innerHTML = strip + `<p class="dev-empty">No variables are known for this element yet. They appear as the model declares them — start variables, form fields, script and decision results, output mappings.</p>`;
      return;
    }
    let html = "";
    for (const s of sections) {
      const rows = s.items.filter((v) => matches(query, v.name, v.detail));
      if (!rows.length) continue;
      html += `<div class="dev-group"><h4 title="${esc(s.hint)}">${esc(s.title)}</h4>`;
      for (const v of rows) {
        const sample = samples.values[v.name];
        const shown = sampleText(sample);
        html += `<button type="button" class="dev-item${shown ? " stacked" : ""}" draggable="true" data-insert="${esc(varInsert(lang.id, v.name))}">
          <span class="dev-item-head">
            <span class="dev-item-name">${esc(v.name)}</span>
            <span class="dev-item-detail">${esc(v.detail)}</span>
          </span>${shown ? `<span class="dev-item-value" title="${esc(String(sample.value))}">= ${esc(shown)}</span>` : ""}</button>`;
      }
      html += `</div>`;
    }
    paneVars.innerHTML = strip + (html || `<p class="dev-empty">No variable matches the filter.</p>`);
  }

  function renderFns() {
    const query = filter.value.trim().toLowerCase();
    let html = "";
    for (const g of groups) {
      const rows = (g.items || []).filter((it) => matches(query, it.name, it.sig, it.doc));
      if (!rows.length) continue;
      html += `<div class="dev-group"><h4>${esc(g.title)}</h4>`;
      for (const it of rows) {
        // The row reads; the "+" beside it inserts. Two targets, no ambiguity about
        // which click edits the code.
        html += `<div class="dev-row">
          <button type="button" class="dev-item dev-fn" data-fn="${esc(it.name)}">
            <span class="dev-item-name">${esc(it.name)}</span>
            <span class="dev-item-detail">${esc(it.sig || "")}</span></button>
          <button type="button" class="dev-quick dev-ins-fn" data-fn="${esc(it.name)}" title="Insert ${esc(it.name)}" aria-label="Insert ${esc(it.name)}">+</button>
        </div>`;
      }
      html += `</div>`;
    }
    paneFns.innerHTML = html || `<p class="dev-empty">No function matches the filter.</p>`;
  }

  function findItem(name) {
    for (const g of groups) {
      const hit = (g.items || []).find((it) => it.name === name);
      if (hit) return hit;
    }
    return null;
  }

  function renderHelp() {
    const it = selectedItem;
    let html = `<div class="dev-help-about"><h4>${esc(lang.label)}</h4><p>${lang.overview}</p></div>`;
    if (it) {
      html += `<div class="dev-help-fn">
        <h4>${esc(it.name)}</h4>
        <pre class="dev-help-sig">${esc(it.sig || it.name)}</pre>
        <p>${esc(it.doc || "")}</p>`;
      if (it.ex) {
        html += `<h5>Example</h5><pre class="dev-help-ex">${esc(it.ex)}</pre>
          <button type="button" class="btn ghost small dev-ins-ex">Insert example</button>`;
      }
      html += `<button type="button" class="btn ghost small dev-ins-fn" data-fn="${esc(it.name)}">Insert call</button></div>`;
    } else if (groups.length) {
      html += `<p class="dev-empty">Pick a function to read its page.</p>`;
    }
    paneHelp.innerHTML = html;
  }

  function renderSnips() {
    const query = filter.value.trim().toLowerCase();
    const rows = snippets.filter((s) => matches(query, s.title, s.doc, s.code));
    paneSnips.innerHTML = rows.length
      ? rows.map((s, i) => `<div class="dev-snip">
          <h4>${esc(s.title)}</h4>
          <p>${esc(s.doc)}</p>
          <pre>${esc(s.code)}</pre>
          <button type="button" class="btn ghost small dev-ins-snip" data-i="${i}">Insert</button>
        </div>`).join("")
      : `<p class="dev-empty">No example matches the filter.</p>`;
  }

  const renderers = { vars: renderVars, fns: renderFns, help: renderHelp, snips: renderSnips };
  let tab = "vars";

  // The side panel folds away to a rail so a wide script can have the whole modal.
  // The choice is remembered across openings — a developer who works without the
  // reference should not re-collapse it every time. A blocked localStorage (private
  // mode, embedded frame) just means the panel opens expanded.
  const side = q(".dev-side");
  const sideToggle = q(".dev-side-toggle");
  const setSide = (collapsed, persist) => {
    side.classList.toggle("collapsed", collapsed);
    // The rail has a width of its own, so an authored panel width steps aside while
    // collapsed and is restored — not forgotten — when the panel comes back.
    if (collapsed) side.style.flex = "";
    else if (sideWidth) setSideWidth(sideWidth, false);
    sideToggle.textContent = collapsed ? "‹" : "›";
    sideToggle.title = collapsed ? "Expand the panel" : "Collapse the panel";
    sideToggle.setAttribute("aria-label", sideToggle.title);
    sideToggle.setAttribute("aria-expanded", collapsed ? "false" : "true");
    if (persist) { try { localStorage.setItem(SIDE_KEY, collapsed ? "collapsed" : "open"); } catch { /* no storage */ } }
  };
  sideToggle.addEventListener("click", () => setSide(!side.classList.contains("collapsed"), true));

  // ---------- layout: splitter, move, resize ----------
  //
  // The three gestures a window is expected to have. All of them are geometry only:
  // nothing here touches the value being edited, so a botched drag can lose a
  // position, never an edit.

  const modal = q(".dev-modal");
  const split = q(".dev-split");
  const head = q(".dev-head");

  const store = (key, value) => { try { localStorage.setItem(key, value); } catch { /* no storage */ } };
  const read = (key) => { try { return localStorage.getItem(key); } catch { return null; } };

  // setSideWidth applies (and optionally remembers) the side panel's width, clamped
  // so neither pane can be squeezed out of existence.
  let sideWidth = 0; // the authored panel width, kept across a collapse/expand
  function setSideWidth(px, persist) {
    const max = Math.max(MIN_SIDE, modal.getBoundingClientRect().width - MIN_MODAL_W / 2);
    const w = Math.round(Math.min(Math.max(px, MIN_SIDE), max));
    sideWidth = w;
    if (!side.classList.contains("collapsed")) side.style.flex = `0 0 ${w}px`;
    if (persist) store(SIDEW_KEY, String(w));
  }

  // place pins the modal at an explicit position and size, taking it out of the
  // overlay's centering grid. x/y are clamped so a strip of the header — the handle
  // that drags it back — always stays on screen.
  function place(x, y, w, h) {
    const rect = modal.getBoundingClientRect();
    const width = Math.max(w === undefined ? rect.width : w, MIN_MODAL_W);
    const height = Math.max(h === undefined ? rect.height : h, MIN_MODAL_H);
    modal.classList.add("placed");
    modal.style.width = width + "px";
    modal.style.height = height + "px";
    modal.style.left = Math.round(Math.min(Math.max(x, KEEP_ON_SCREEN - width), window.innerWidth - KEEP_ON_SCREEN)) + "px";
    modal.style.top = Math.round(Math.min(Math.max(y, 0), window.innerHeight - KEEP_ON_SCREEN)) + "px";
  }

  let geomTimer = null; // pending debounced save, flushed when the view closes

  // saveGeometry records the modal's box — but never a degenerate one. A detached
  // modal measures 0×0, and storing that would silently disable the whole feature:
  // the restore path skips a zero-sized geometry, so the window would come back at
  // the default size forever after.
  function saveGeometry() {
    if (!modal.classList.contains("placed") || !modal.isConnected) return;
    const r = modal.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0) return;
    store(GEOM_KEY, JSON.stringify({ x: Math.round(r.left), y: Math.round(r.top), w: Math.round(r.width), h: Math.round(r.height) }));
  }

  // flushGeometry runs a pending save now. Closing must not lose a resize: nobody
  // waits out a debounce before pressing Escape, and a timer that fires after the
  // overlay is gone measures nothing.
  function flushGeometry() {
    clearTimeout(geomTimer);
    geomTimer = null;
    saveGeometry();
  }

  // resetLayout returns the modal to the centered default and forgets the stored
  // geometry — the way out of an arrangement that made sense on another screen.
  function resetLayout() {
    modal.classList.remove("placed");
    modal.style.left = modal.style.top = modal.style.width = modal.style.height = "";
    try { localStorage.removeItem(GEOM_KEY); } catch { /* no storage */ }
  }

  // drag is the shared pointer-drag loop: capture the pointer so the gesture
  // survives leaving the element, run onMove with the delta, and finish once.
  function drag(e, onMove, onDone) {
    e.preventDefault();
    const startX = e.clientX;
    const startY = e.clientY;
    const target = e.currentTarget;
    try { target.setPointerCapture(e.pointerId); } catch { /* not captureable */ }
    const move = (ev) => onMove(ev.clientX - startX, ev.clientY - startY, ev);
    const up = () => {
      target.removeEventListener("pointermove", move);
      target.removeEventListener("pointerup", up);
      target.removeEventListener("pointercancel", up);
      if (onDone) onDone();
    };
    target.addEventListener("pointermove", move);
    target.addEventListener("pointerup", up);
    target.addEventListener("pointercancel", up);
  }

  // The splitter: drag the divider, or nudge it with the arrow keys once focused.
  split.addEventListener("pointerdown", (e) => {
    const startWidth = side.getBoundingClientRect().width;
    split.classList.add("dragging");
    drag(e, (dx) => setSideWidth(startWidth - dx, false), () => {
      split.classList.remove("dragging");
      setSideWidth(side.getBoundingClientRect().width, true);
    });
  });
  split.addEventListener("keydown", (e) => {
    const step = e.key === "ArrowLeft" ? 16 : e.key === "ArrowRight" ? -16 : 0;
    if (!step) return;
    e.preventDefault();
    setSideWidth(side.getBoundingClientRect().width + step, true);
  });

  // The header moves the whole modal — but only where it is chrome: a drag that
  // starts on a button or the title text selection is not a move.
  head.addEventListener("pointerdown", (e) => {
    if (e.target.closest("button, input, select, a")) return;
    const r = modal.getBoundingClientRect();
    place(r.left, r.top, r.width, r.height);
    modal.classList.add("moving");
    drag(e, (dx, dy) => place(r.left + dx, r.top + dy), () => {
      modal.classList.remove("moving");
      saveGeometry();
    });
  });
  head.addEventListener("dblclick", (e) => {
    if (e.target.closest("button, input, select, a")) return;
    resetLayout();
  });

  // The modal itself carries a native resize grip (CSS `resize: both`). A native
  // resize writes inline width/height, which is also how we tell a user's resize
  // from the initial layout — so only the former is remembered.
  if (typeof ResizeObserver === "function") {
    new ResizeObserver(() => {
      if (!modal.style.width) return; // the browser's own first layout, not a resize
      if (!modal.classList.contains("placed")) {
        const r = modal.getBoundingClientRect();
        place(r.left, r.top, r.width, r.height);
      }
      clearTimeout(geomTimer);
      geomTimer = setTimeout(saveGeometry, 200);
    }).observe(modal);
  }

  // A window that shrank under a remembered geometry must not strand the modal
  // off-screen.
  const onWindowResize = () => {
    if (!modal.classList.contains("placed")) return;
    const r = modal.getBoundingClientRect();
    place(r.left, r.top, Math.min(r.width, window.innerWidth), Math.min(r.height, window.innerHeight));
  };
  window.addEventListener("resize", onWindowResize);

  function showTab(next) {
    tab = next;
    // Picking a tab from the collapsed rail is a request to read it.
    if (side.classList.contains("collapsed")) setSide(false, true);
    for (const b of overlay.querySelectorAll(".dev-tab")) {
      const on = b.dataset.tab === next;
      b.classList.toggle("active", on);
      b.setAttribute("aria-selected", on ? "true" : "false");
    }
    paneVars.hidden = next !== "vars";
    paneFns.hidden = next !== "fns";
    paneHelp.hidden = next !== "help";
    paneSnips.hidden = next !== "snips";
    filter.hidden = next === "help";
    renderers[next]();
  }

  // ---------- interactions ----------

  overlay.addEventListener("click", (e) => {
    const tabBtn = e.target.closest(".dev-tab");
    if (tabBtn) { showTab(tabBtn.dataset.tab); return; }

    // Clicking a function in the catalogue *reads* it — the pane is a reference, and
    // a reader who is browsing must not have their code edited underneath them.
    // Inserting is the "+" beside the row, or the Help page's button.
    const row = e.target.closest(".dev-fn");
    if (row) {
      const item = findItem(row.dataset.fn);
      if (!item) return;
      selectedItem = item;
      showTab("help");
      return;
    }
    const ins = e.target.closest(".dev-ins-fn");
    if (ins) { insertFn(findItem(ins.dataset.fn)); return; }
    const ex = e.target.closest(".dev-ins-ex");
    if (ex && selectedItem && selectedItem.ex) { insertText(ta, selectedItem.ex); return; }

    const snip = e.target.closest(".dev-ins-snip");
    if (snip) { insertText(ta, snippets[Number(snip.dataset.i)].code); return; }

    if (e.target.closest(".dev-sample-reload")) { loadSamples({ force: true, instanceKey: samples.instance && samples.instance.key }); return; }

    const item = e.target.closest(".dev-item:not(.dev-fn)");
    if (item) { insertText(ta, item.dataset.insert); return; }
  });

  // insertFn writes a reference item into the editor: a function as a call with the
  // caret between the parentheses, a keyword/operator as the skeleton it declares.
  function insertFn(item) {
    if (!item) return;
    if (item.insert) insertText(ta, item.insert);
    else if (item.call !== false) insertText(ta, item.name + "()", item.name.length + 1);
    else insertText(ta, item.name);
  }

  // Dragging a variable onto the editor is the same gesture as in the Variables
  // panel — code-editor.js already accepts a text/plain drop at the drop point.
  paneVars.addEventListener("dragstart", (e) => {
    const item = e.target.closest(".dev-item");
    if (!item || !e.dataTransfer) return;
    e.dataTransfer.setData("text/plain", item.dataset.insert || "");
    e.dataTransfer.effectAllowed = "copy";
  });

  // Picking another instance re-reads the values from it. The caller answers from
  // its own cache, so this is normally not a request.
  paneVars.addEventListener("change", (e) => {
    const pick = e.target.closest(".dev-sample-pick");
    if (pick) loadSamples({ instanceKey: Number(pick.value) });
  });

  filter.addEventListener("input", () => renderers[tab]());

  const dirtyEl = q(".dev-dirty");
  const posEl = q(".dev-pos");
  const countEl = q(".dev-count");
  const confirmEl = q(".dev-confirm");

  function refreshStatus() {
    const dirty = ta.value !== original;
    dirtyEl.hidden = !dirty;
    const upto = ta.value.slice(0, ta.selectionStart ?? 0);
    const lines = upto.split("\n");
    posEl.textContent = `Ln ${lines.length}, Col ${lines[lines.length - 1].length + 1}`;
    const total = ta.value ? ta.value.split("\n").length : 1;
    countEl.textContent = `${total} line${total === 1 ? "" : "s"} · ${ta.value.length} char${ta.value.length === 1 ? "" : "s"}`;
  }
  ta.addEventListener("input", refreshStatus);
  ta.addEventListener("keyup", refreshStatus);
  ta.addEventListener("click", refreshStatus);

  // ---------- Test panel ----------

  if (canTest) {
    const runWrap = q(".dev-run");
    const toggle = q(".dev-test-toggle");
    toggle.addEventListener("click", () => {
      runWrap.hidden = !runWrap.hidden;
      toggle.classList.toggle("active", !runWrap.hidden);
      toggle.setAttribute("aria-pressed", runWrap.hidden ? "false" : "true");
    });
    const out = q(".dev-run-out");
    const detail = q(".dev-run-detail");
    const setOut = (cls, text) => { out.className = "dev-run-out" + (cls ? " " + cls : ""); out.textContent = text; };
    const setDetail = (cls, text) => {
      detail.hidden = !text;
      detail.className = "dev-run-detail" + (cls ? " " + cls : "");
      detail.textContent = text || "";
    };
    const fillBtn = q(".dev-run-fill");
    fillBtn.title = "Replace the sample with this instance's actual variables";
    fillBtn.addEventListener("click", () => {
      lastFilled = JSON.stringify(sampleJSON(samples.values), null, 2);
      q(".dev-run-vars").value = lastFilled;
    });
    q(".dev-run-btn").addEventListener("click", async () => {
      let vars = {};
      const raw = (q(".dev-run-vars").value || "").trim();
      if (raw) {
        try { vars = JSON.parse(raw); }
        catch { setOut("err", "Sample variables must be valid JSON."); setDetail("", ""); return; }
      }
      setOut("", "Running…"); setDetail("", "");
      const t0 = performance.now();
      try {
        const src = ta.value.slice(opts.lockPrefix ? opts.lockPrefix(ta.value) : 0);
        const r = await (opts.evaluate ? opts.evaluate(src, vars) : opts.run(src, vars));
        const ms = Math.round(performance.now() - t0);
        if (r && r.ok) {
          setOut("ok", `✓ ${ms} ms`);
          let shown;
          try { shown = JSON.stringify(r.result === undefined ? null : r.result, null, 2); }
          catch { shown = String(r.result); }
          // The FEEL evaluator also reports the value's type; a script run doesn't.
          setDetail("ok", "→ " + shown + (r.kind ? `   (${r.kind})` : ""));
        } else {
          setOut("err", `✗ ${ms} ms`);
          setDetail("err", (r && r.error) || "run failed");
        }
      } catch (err) {
        setOut("err", "✗");
        setDetail("err", (err && err.message) || String(err));
      }
    });
  }

  // ---------- apply / close ----------

  function apply() {
    field.value = ta.value;
    // The same events a keystroke produces: `input` for anything listening live,
    // `change` for the panel's save-on-blur wiring.
    field.dispatchEvent(new Event("input", { bubbles: true }));
    field.dispatchEvent(new Event("change", { bubbles: true }));
    if (typeof opts.onApply === "function") { try { opts.onApply(ta.value); } catch { /* caller's problem */ } }
    close();
  }

  function requestClose() {
    if (ta.value === original) { close(); return; }
    // Losing a screenful of code to a stray Esc is the one failure this modal must
    // not have: the first Esc asks, the second discards.
    confirmEl.hidden = false;
    q(".dev-keep").focus();
  }

  function close() {
    if (openView !== handle) return;
    openView = null;
    flushGeometry(); // while the modal is still measurable
    document.removeEventListener("keydown", onKeydown, true);
    window.removeEventListener("resize", onWindowResize);
    try { if (editor) editor.destroy(); } catch { /* already gone */ }
    overlay.remove();
    if (restoreFocus && document.contains(restoreFocus)) {
      try { restoreFocus.focus(); } catch { /* best-effort */ }
    }
  }

  function onKeydown(e) {
    if (e.key === "Escape") {
      // An open completion popup owns Escape first — it closes the popup, not the
      // modal. That is the editor's handler, so we simply stay out of the way.
      if (overlay.querySelector(".feel-pop:not([hidden])")) return;
      e.preventDefault();
      e.stopPropagation();
      if (!confirmEl.hidden) close(); else requestClose();
      return;
    }
    if (e.key === "F2" && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey) {
      // F2 is a toggle: it opened the view, and it takes the work back to the field.
      e.preventDefault();
      e.stopPropagation();
      apply();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && (e.key === "Enter" || e.key === "s")) {
      e.preventDefault();
      e.stopPropagation();
      apply();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && (e.key === "F" || e.key === "f") && lang.format) {
      e.preventDefault();
      e.stopPropagation();
      format();
    }
  }

  function format() {
    if (!lang.format) return;
    const next = lang.format(ta.value);
    if (next === ta.value) return;
    ta.value = next;
    ta.dispatchEvent(new Event("input", { bubbles: true }));
    refreshStatus();
  }

  q(".dev-apply").addEventListener("click", apply);
  q(".dev-cancel").addEventListener("click", requestClose);
  q(".dev-discard").addEventListener("click", close);
  q(".dev-keep").addEventListener("click", () => { confirmEl.hidden = true; ta.focus(); });
  if (lang.format) q(".dev-fmt").addEventListener("click", format);
  // Capture so Esc reaches us before the code editor's own handler closes a popup
  // only — and before any host page binding.
  document.addEventListener("keydown", onKeydown, true);

  const handle = { close, apply, el: overlay, editor };
  openView = handle;

  // Restore the remembered layout: panel width first (it is independent of the
  // modal's own box), then size and position, clamped into the window as it is now.
  const storedWidth = Number(read(SIDEW_KEY));
  if (storedWidth > 0) setSideWidth(storedWidth, false);
  try {
    const g = JSON.parse(read(GEOM_KEY) || "null");
    if (g && g.w > 0 && g.h > 0) {
      place(g.x, g.y, Math.min(g.w, window.innerWidth), Math.min(g.h, window.innerHeight));
    }
  } catch { /* a corrupt entry just means the default layout */ }

  let startCollapsed = false;
  try { startCollapsed = localStorage.getItem(SIDE_KEY) === "collapsed"; } catch { /* no storage */ }
  showTab("vars");
  setSide(startCollapsed, false);
  loadSamples({}); // lazy and best-effort: the modal is already usable
  renderHelp();
  refreshStatus();
  ta.focus();
  ta.setSelectionRange(ta.value.length, ta.value.length);
  return handle;
}
