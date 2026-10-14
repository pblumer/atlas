// The FEEL assistant (ADR-0445): a conversation with the AI Worker an
// operator configured that writes a FEEL expression, an editor and a test pane to try
// it in, and a history and favourites to come back to it.
//
// It is opened from anywhere in the console — Ctrl/⌘+Shift+E, the spark in the top
// bar, or the mini spark on a FEEL field — and when it is opened from a field it knows
// that field: the field's expression is what it starts from, and "Apply to field" writes
// the result back through the field's own input and change events, exactly as the
// Developer View does (ADR-0145). Everywhere else the expression is copied.
//
// What the AI Worker writes has been compiled and evaluated by the engine before it
// arrives here (api/feelgen); this module shows that verdict beside it and never hides
// a failed one. Nothing is stored on the server: the conversation lives for the browser
// session, and the history and favourites live in this browser's storage — they are a
// person's scratch pad, not a shared library.
//
// Buildless like the rest of the console (ADR-0012): no framework, the shared code
// editor, the message catalogue for every word a person reads (ADR-0267).

import { attachFeelEditor, FEEL_ASSISTANT_EVENT, SPARK_SVG, offerFeelAssistant } from "./feel.js";
import { t } from "./i18n.js";

const SESSION_KEY = "atlas.feel.session";
const HISTORY_KEY = "atlas.feel.history";
const FAVOURITES_KEY = "atlas.feel.favourites";

// HISTORY_MAX bounds the history. It is a scratch pad: the last thirty expressions are
// what somebody comes back for, and favourites are where the ones worth keeping go.
export const HISTORY_MAX = 30;

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// ---------- storage ----------

// Every read and write is guarded: storage can be absent (a private window), full, or
// switched off, and the assistant must work without it — it only forgets more.
function store(kind) {
  try { return kind === "session" ? window.sessionStorage : window.localStorage; } catch { return null; }
}
function readJSON(kind, key, fallback) {
  try {
    const raw = store(kind) && store(kind).getItem(key);
    const v = raw ? JSON.parse(raw) : null;
    return v ?? fallback;
  } catch { return fallback; }
}
function writeJSON(kind, key, value) {
  try { const s = store(kind); if (s) s.setItem(key, JSON.stringify(value)); } catch { /* nothing to keep it in */ }
}

const newId = () => Date.now().toString(36) + Math.random().toString(36).slice(2, 7);

// noteFor shortens a sentence to a list label.
function noteFor(text) {
  const s = String(text || "").replace(/\s+/g, " ").trim();
  return s.length > 90 ? s.slice(0, 89) + "…" : s;
}

export function loadHistory() {
  const list = readJSON("local", HISTORY_KEY, []);
  return Array.isArray(list) ? list : [];
}

export function loadFavourites() {
  const list = readJSON("local", FAVOURITES_KEY, []);
  return Array.isArray(list) ? list : [];
}

// remember puts an expression at the top of the history. The same expression is one
// entry however often it is run or copied: the history is of expressions, not of
// clicks.
export function remember({ expression, variables = "", note = "" }) {
  const expr = String(expression || "").trim();
  if (!expr) return loadHistory();
  const prior = loadHistory().find((e) => e.expression === expr);
  const entry = {
    id: (prior && prior.id) || newId(),
    expression: expr,
    variables: variables || (prior && prior.variables) || "",
    note: noteFor(note) || (prior && prior.note) || "",
    at: Date.now(),
  };
  const list = [entry, ...loadHistory().filter((e) => e.expression !== expr)].slice(0, HISTORY_MAX);
  writeJSON("local", HISTORY_KEY, list);
  return list;
}

export function removeHistory(id) {
  writeJSON("local", HISTORY_KEY, loadHistory().filter((e) => e.id !== id));
}

export function clearHistory() {
  writeJSON("local", HISTORY_KEY, []);
}

export function isFavourite(expression) {
  const expr = String(expression || "").trim();
  return !!expr && loadFavourites().some((f) => f.expression === expr);
}

// toggleFavourite saves the expression as a favourite, or removes it when it already is
// one, and reports which it now is.
export function toggleFavourite({ expression, variables = "", name = "" }) {
  const expr = String(expression || "").trim();
  if (!expr) return false;
  const list = loadFavourites();
  if (list.some((f) => f.expression === expr)) {
    writeJSON("local", FAVOURITES_KEY, list.filter((f) => f.expression !== expr));
    return false;
  }
  list.unshift({ id: newId(), name: noteFor(name) || noteFor(expr), expression: expr, variables, at: Date.now() });
  writeJSON("local", FAVOURITES_KEY, list);
  return true;
}

export function renameFavourite(id, name) {
  const n = noteFor(name);
  if (!n) return;
  writeJSON("local", FAVOURITES_KEY, loadFavourites().map((f) => (f.id === id ? { ...f, name: n } : f)));
}

export function removeFavourite(id) {
  writeJSON("local", FAVOURITES_KEY, loadFavourites().filter((f) => f.id !== id));
}

// ---------- the field it was opened from ----------

// fieldTitle names a Modeler field the way the Developer View does: an explicit
// data-devtitle, else the label it sits under, else its accessible name.
function fieldTitle(field) {
  if (field.dataset.devtitle) return field.dataset.devtitle;
  const label = field.closest("label");
  const span = label && label.querySelector("span");
  const text = span && span.textContent.trim();
  if (text) return text;
  return field.getAttribute("aria-label") || field.placeholder || t("feel.target.field");
}

// feelTarget describes the FEEL field an element belongs to, or null when it belongs to
// none. A target says what to call the field, what to tell the model about it, what it
// holds now, and whether the assistant may write into it.
//
//   - A Modeler field the Developer View knows as FEEL (data-devlang="feel"). An fx
//     field keeps its "=" marker as a prefix the assistant never shows and always
//     writes back (ADR-0067).
//   - A cell or editor of dmn-js: an output cell, a literal expression, a column's
//     input expression. An input cell is recognised too, and is not written to: it
//     takes a unary test, which is not an expression and which this assistant neither
//     writes nor checks — putting a whole condition there makes the rule never fire.
export function feelTarget(el) {
  if (!el || !el.closest) return null;
  const field = el.closest('[data-devlang="feel"]');
  if (field && (field.tagName === "TEXTAREA" || field.tagName === "INPUT")) {
    const fx = field.dataset.fxOn === "1" || /^\s*=/.test(field.value);
    const prefix = fx ? ((/^\s*=\s*/.exec(field.value) || [""])[0] || "=") : "";
    const name = fieldTitle(field);
    return {
      kind: "field", el: field, prefix, name, applicable: true,
      value: field.value.slice(prefix.length),
      prompt: t("feel.target.fieldPrompt", { name }),
    };
  }
  const editable = el.closest('[contenteditable]:not([contenteditable="false"])');
  if (!editable) return null;
  const cell = editable.closest("td");
  const value = (editable.innerText || editable.textContent || "").trim();
  const dmn = (kind, key, applicable = true) => ({
    kind, el: editable, prefix: "", applicable, value,
    name: t("feel.target." + key), prompt: t("feel.target." + key + "Prompt"),
  });
  if (cell && cell.classList.contains("input-cell")) return dmn("dmn-input", "dmnInput", false);
  if (cell && cell.classList.contains("output-cell")) return dmn("dmn-output", "dmnOutput");
  if (editable.closest(".ref-text")) return dmn("dmn-expression", "dmnExpression");
  if (editable.closest(".dmn-literal-expression-container .textarea")) return dmn("dmn-literal", "dmnLiteral");
  return null;
}

// applyTo writes an expression into the target the assistant was opened from, and
// reports whether it could. A field is written as the Developer View writes one — the
// value, then the input and change events the panel's save wiring listens for. A dmn-js
// editor is written as typing would: its contents selected and replaced through the
// editing command, so dmn-js sees an input it records and can undo; where the browser
// refuses the command, the text is set and the input event fired by hand.
export function applyTo(target, expression) {
  if (!target || !target.applicable || !target.el || !target.el.isConnected) return false;
  const el = target.el;
  if (target.kind === "field") {
    el.value = (target.prefix || "") + expression;
    el.dispatchEvent(new Event("input", { bubbles: true }));
    el.dispatchEvent(new Event("change", { bubbles: true }));
    return true;
  }
  el.focus();
  const sel = window.getSelection();
  const range = document.createRange();
  range.selectNodeContents(el);
  sel.removeAllRanges();
  sel.addRange(range);
  let done = false;
  try { done = document.execCommand("insertText", false, expression); } catch { done = false; }
  if (!done) {
    el.textContent = expression;
    el.dispatchEvent(new InputEvent("input", { bubbles: true, inputType: "insertText", data: expression }));
  }
  return true;
}

// ---------- session ----------

// The conversation and the editor survive closing the assistant and reloading the page
// within this tab; a new tab starts a new one. Only what the server can be sent again is
// kept: the turns, and per assistant turn the proposal it showed.
const session = Object.assign(
  { messages: [], expression: "", variables: "", worker: "", tab: "history" },
  readJSON("session", SESSION_KEY, {}),
);
if (!Array.isArray(session.messages)) session.messages = [];

function saveSession() {
  writeJSON("session", SESSION_KEY, session);
}

// parseVariables reads the test pane: empty is no variables, anything else must be a
// JSON object, because variables bind by name.
export function parseVariables(text) {
  const s = String(text || "").trim();
  if (!s) return undefined;
  const v = JSON.parse(s);
  if (!v || typeof v !== "object" || Array.isArray(v)) throw new Error(t("feel.vars.invalid"));
  return v;
}

// checkLine renders the engine's verdict on a proposal as one line and a tone.
export function checkLine(check) {
  if (!check) return { tone: "", text: "" };
  if (!check.ok) return { tone: "err", text: t("feel.check.error", { error: check.error || "?" }) };
  const parts = [t("feel.check.ok", { result: check.result, kind: check.kind })];
  let tone = "ok";
  if (check.matches === true) parts.push(t("feel.check.matches"));
  if (check.matches === false) { parts.push(t("feel.check.mismatch", { expected: check.expected })); tone = "warn"; }
  if (check.missing && check.missing.length) { parts.push(t("feel.check.missing", { names: check.missing.join(", ") })); tone = "warn"; }
  return { tone, text: parts.join(" · ") };
}

// ---------- the assistant ----------

let live = null; // the open assistant, if any
let capability = null; // the AI Worker probe, asked once per page

function workersOf(api) {
  if (!capability) {
    capability = Promise.resolve()
      .then(() => api("GET", "/api/v1/feel/generate/workers"))
      .then((c) => (c && c.available && Array.isArray(c.workers) ? c.workers : []))
      .catch(() => []);
  }
  return capability;
}

export function feelAssistantOpen() {
  return !!live;
}

export function closeFeelAssistant() {
  if (live) live.close();
}

// openFeelAssistant shows the assistant. opts.api is the console's api(), opts.copy its
// clipboard writer, opts.toast its notifier, and opts.target the field it was opened
// from (feelTarget), if any.
export function openFeelAssistant(opts = {}) {
  if (live) { live.focus(); return live; }
  const api = opts.api;
  const copy = opts.copy || (async (text) => { try { await navigator.clipboard.writeText(text); return true; } catch { return false; } });
  const toast = opts.toast || (() => {});
  const target = opts.target || null;
  const returnTo = document.activeElement;

  // Opening from a field that holds an expression starts from that expression: the
  // field is what the author is working on. Whatever the editor held before goes to
  // the history rather than being lost.
  if (target && target.value && target.value.trim() && target.value.trim() !== session.expression.trim()) {
    if (session.expression.trim()) remember({ expression: session.expression, variables: session.variables });
    session.expression = target.value.trim();
  }

  const ov = document.createElement("div");
  ov.className = "fa-overlay";
  ov.innerHTML = `
    <div class="fa-modal" role="dialog" aria-modal="true" aria-label="${esc(t("feel.title"))}">
      <div class="fa-head">
        <span class="fa-mark" aria-hidden="true">${SPARK_SVG}</span>
        <h2 class="fa-title">${esc(t("feel.title"))}</h2>
        ${target ? `<span class="fa-target" data-target>${esc(t("feel.for", { name: target.name }))}</span>` : ""}
        <span class="fa-sp"></span>
        <span class="fa-worker" data-worker></span>
        <button type="button" class="btn ghost small" data-new>${esc(t("feel.new"))}</button>
        <button type="button" class="icon-btn" data-close aria-label="${esc(t("feel.close"))}" title="${esc(t("feel.close"))}">✕</button>
      </div>
      <div class="fa-body">
        <section class="fa-chat">
          <div class="fa-log" data-log aria-live="polite"></div>
          <p class="fa-status" data-status hidden></p>
          <div class="fa-ask">
            <textarea data-ask rows="3" spellcheck="true" placeholder="${esc(t("feel.chat.placeholder"))}"></textarea>
            <button type="button" class="btn" data-send>${esc(t("feel.chat.send"))}</button>
          </div>
        </section>
        <section class="fa-work">
          <label class="fa-label" for="fa-expr">${esc(t("feel.editor.label"))}</label>
          <textarea id="fa-expr" data-expr rows="4" spellcheck="false"></textarea>
          <label class="fa-label" for="fa-vars">${esc(t("feel.vars.label"))}</label>
          <textarea id="fa-vars" class="fa-vars" data-vars rows="4" spellcheck="false" placeholder='{ "amount": 100 }'></textarea>
          <div class="fa-actions">
            <button type="button" class="btn neutral" data-run>${esc(t("feel.run"))}</button>
            <span class="fa-out" data-out aria-live="polite"></span>
            <span class="fa-sp"></span>
            <button type="button" class="icon-btn fa-star" data-star></button>
            <button type="button" class="btn neutral" data-copy>${esc(t("feel.copy"))}</button>
            ${target ? `<button type="button" class="btn" data-apply>${esc(t("feel.apply"))}</button>` : ""}
          </div>
          ${target && !target.applicable ? `<p class="fa-note warn" data-apply-note>${esc(t("feel.apply.unaryTests"))}</p>` : ""}
          <div class="fa-lists">
            <div class="fa-tabs" role="tablist">
              <button type="button" role="tab" data-tab="history"></button>
              <button type="button" role="tab" data-tab="favourites"></button>
              <span class="fa-sp"></span>
              <span class="fa-local">${esc(t("feel.list.local"))}</span>
            </div>
            <ul class="fa-list" data-list></ul>
          </div>
        </section>
      </div>
    </div>`;
  document.body.appendChild(ov);

  const $ = (sel) => ov.querySelector(sel);
  const logEl = $("[data-log]");
  const statusEl = $("[data-status]");
  const askEl = $("[data-ask]");
  const sendBtn = $("[data-send]");
  const exprEl = $("[data-expr]");
  const varsEl = $("[data-vars]");
  const outEl = $("[data-out]");
  const starBtn = $("[data-star]");
  const listEl = $("[data-list]");
  const workerEl = $("[data-worker]");
  const applyBtn = $("[data-apply]");
  if (applyBtn && !target.applicable) applyBtn.disabled = true;

  exprEl.value = session.expression;
  varsEl.value = session.variables;
  const editor = attachFeelEditor(exprEl, {
    assistant: false,
    validate: api ? (expression) => api("POST", "/api/v1/feel/validate", { expression }) : undefined,
  });

  let workers = [];
  let probed = !api; // without an api there is nothing to ask, and nothing to wait for
  let busy = false;
  // self is this opening of the assistant. A request can outlive it — the author
  // closes the assistant, or closes and opens it again, while the AI Worker is still
  // writing — and its answer must then go to the session, not into the elements of a
  // dialog that is no longer on screen.
  const self = {};
  const isLive = () => live === self;

  const setStatus = (tone, text) => {
    statusEl.hidden = !text;
    statusEl.className = "fa-status" + (tone ? " " + tone : "");
    statusEl.textContent = text || "";
  };
  const setOut = (tone, text) => {
    outEl.className = "fa-out" + (tone ? " " + tone : "");
    outEl.textContent = text || "";
  };
  const setEditor = (expression, variables) => {
    exprEl.value = expression;
    // The same event a keystroke produces, so the highlighter and the validator follow.
    exprEl.dispatchEvent(new Event("input", { bubbles: true }));
    if (variables !== undefined) varsEl.value = variables;
    persist();
  };
  const current = () => exprEl.value.trim();

  function persist() {
    session.expression = exprEl.value;
    session.variables = varsEl.value;
    saveSession();
    paintStar();
  }

  function paintStar() {
    const fav = isFavourite(current());
    starBtn.textContent = fav ? "★" : "☆";
    starBtn.classList.toggle("on", fav);
    const label = t(fav ? "feel.favourite.remove" : "feel.favourite.add");
    starBtn.title = label;
    starBtn.setAttribute("aria-label", label);
    starBtn.setAttribute("aria-pressed", fav ? "true" : "false");
  }

  function paintWorker() {
    if (!workers.length) { workerEl.innerHTML = ""; return; }
    if (workers.length === 1) {
      const w = workers[0];
      workerEl.textContent = `${t("feel.chat.worker")}: ${w.name}${w.model ? " · " + w.model : ""}`;
      return;
    }
    // Several AI Workers: the author chooses, because the choice is what the answer
    // costs and how good it is (ADR-0260).
    if (!workers.some((w) => w.name === session.worker)) session.worker = workers[0].name;
    workerEl.innerHTML = `<label>${esc(t("feel.chat.worker"))} <select data-worker-pick>${workers.map((w) =>
      `<option value="${esc(w.name)}" ${w.name === session.worker ? "selected" : ""}>${esc(w.name)}${w.model ? " · " + esc(w.model) : ""}</option>`).join("")}</select></label>`;
    workerEl.querySelector("[data-worker-pick]").addEventListener("change", (e) => { session.worker = e.target.value; saveSession(); });
  }

  function paintLog() {
    if (!session.messages.length) {
      logEl.innerHTML = `<p class="fa-intro">${esc(t(workers.length || !probed ? "feel.chat.intro" : "feel.chat.unavailable"))}</p>`;
      return;
    }
    logEl.innerHTML = session.messages.map((m, i) => {
      if (m.role === "user") return `<div class="fa-msg user">${esc(m.content)}</div>`;
      const v = m.view || {};
      const line = checkLine(v.check);
      const meta = [];
      if (v.attempts > 1) meta.push(t("feel.chat.attempts", { n: v.attempts }));
      if (v.worker) meta.push(v.worker + (v.model ? " · " + v.model : ""));
      if (v.prompt) meta.push(t("feel.chat.prompt", { v: v.prompt }));
      return `<div class="fa-msg ai">
        ${v.explanation ? `<div class="fa-expl">${esc(v.explanation)}</div>` : ""}
        ${v.expression ? `<pre class="fa-code">${esc(v.expression)}</pre>` : ""}
        ${line.text ? `<div class="fa-check ${line.tone}">${esc(line.text)}</div>` : ""}
        ${v.warning ? `<div class="fa-check warn">${esc(t("feel.chat.warning", { warning: v.warning }))}</div>` : ""}
        <div class="fa-msg-foot">
          <span class="fa-meta">${esc(meta.join(" · "))}</span>
          ${v.expression ? `<button type="button" class="linklike" data-load="${i}">${esc(t("feel.chat.load"))}</button>` : ""}
        </div>
      </div>`;
    }).join("");
    logEl.scrollTop = logEl.scrollHeight;
  }

  function paintChatState() {
    const can = workers.length > 0;
    askEl.disabled = !can || busy;
    sendBtn.disabled = !can || busy;
  }

  function paintLists() {
    const history = loadHistory();
    const favourites = loadFavourites();
    for (const tab of ov.querySelectorAll("[data-tab]")) {
      const id = tab.dataset.tab;
      const n = id === "history" ? history.length : favourites.length;
      tab.textContent = `${t(id === "history" ? "feel.history" : "feel.favourites")} (${n})`;
      tab.setAttribute("aria-selected", id === session.tab ? "true" : "false");
      tab.classList.toggle("on", id === session.tab);
    }
    const items = session.tab === "favourites" ? favourites : history;
    if (!items.length) {
      listEl.innerHTML = `<li class="fa-empty">${esc(t(session.tab === "favourites" ? "feel.list.emptyFavourites" : "feel.list.emptyHistory"))}</li>`;
      return;
    }
    const fav = session.tab === "favourites";
    listEl.innerHTML = items.map((e) => `
      <li data-id="${esc(e.id)}">
        <button type="button" class="fa-pick" data-pick="${esc(e.id)}" title="${esc(e.expression)}">
          <span class="fa-pick-name">${esc(fav ? e.name : (e.note || e.expression))}</span>
          <code class="fa-pick-expr">${esc(e.expression)}</code>
        </button>
        ${fav ? `<button type="button" class="icon-btn" data-rename="${esc(e.id)}" title="${esc(t("feel.list.rename"))}" aria-label="${esc(t("feel.list.rename"))}">✎</button>` : ""}
        <button type="button" class="icon-btn" data-remove="${esc(e.id)}" title="${esc(t("feel.list.remove"))}" aria-label="${esc(t("feel.list.remove"))}">✕</button>
      </li>`).join("") +
      (fav ? "" : `<li class="fa-list-foot"><button type="button" class="linklike" data-clear>${esc(t("feel.list.clear"))}</button></li>`);
  }

  async function send() {
    const text = askEl.value.trim();
    if (!text || busy || !workers.length) return;
    let variables;
    try { variables = parseVariables(varsEl.value); } catch { setStatus("err", t("feel.vars.invalid")); return; }
    session.messages.push({ role: "user", content: text });
    askEl.value = "";
    busy = true;
    paintLog(); paintChatState();
    setStatus("busy", t("feel.chat.thinking"));
    const body = {
      messages: session.messages.map(({ role, content }) => ({ role, content })),
      expression: exprEl.value,
      target: target ? target.prompt : "",
    };
    if (variables !== undefined) body.variables = variables;
    if (workers.length > 1) body.worker = session.worker;
    try {
      const resp = await api("POST", "/api/v1/feel/generate", body);
      session.messages.push({
        role: "assistant", content: resp.reply || "",
        view: {
          expression: resp.expression, explanation: resp.explanation, check: resp.check || null,
          variables: resp.variables, attempts: resp.attempts, warning: resp.warning,
          worker: resp.worker, model: resp.model, prompt: resp.prompt,
        },
      });
      setStatus("", "");
      if (resp.expression) {
        const vars = resp.variables ? JSON.stringify(resp.variables, null, 2) : varsEl.value;
        if (isLive()) setEditor(resp.expression, vars);
        else { session.expression = resp.expression; session.variables = vars; }
        remember({ expression: resp.expression, variables: vars, note: text });
      }
    } catch (err) {
      // The message that failed goes back where it was typed, with the reason next
      // to it: retyping it is the one thing a failure must not cost.
      session.messages.pop();
      if (isLive()) askEl.value = text;
      setStatus("err", t("feel.chat.failed", { error: (err && err.message) || String(err) }));
    } finally {
      busy = false;
      saveSession();
      if (isLive()) { paintLog(); paintChatState(); paintLists(); askEl.focus(); }
    }
  }

  async function run() {
    const expression = current();
    if (!expression) { setOut("err", t("feel.empty")); return; }
    let variables;
    try { variables = parseVariables(varsEl.value) || {}; } catch { setOut("err", t("feel.vars.invalid")); return; }
    setOut("", "…");
    try {
      const r = await api("POST", "/api/v1/feel/evaluate", { expression, variables });
      if (r && r.ok) {
        setOut("ok", `→ ${r.result} (${r.kind})`);
        remember({ expression, variables: varsEl.value });
        paintLists();
      } else setOut("err", (r && r.error) || "?");
    } catch (err) {
      setOut("err", (err && err.message) || String(err));
    }
  }

  async function doCopy() {
    const expression = current();
    if (!expression) { setOut("err", t("feel.empty")); return; }
    const ok = await copy(expression);
    toast(t(ok ? "feel.copied" : "feel.copyFailed"), ok ? "" : "err");
    if (ok) { remember({ expression, variables: varsEl.value }); paintLists(); }
  }

  function doApply() {
    const expression = current();
    if (!expression) { setOut("err", t("feel.empty")); return; }
    if (!applyTo(target, expression)) { setOut("err", t("feel.apply.gone")); return; }
    remember({ expression, variables: varsEl.value });
    toast(t("feel.applied", { name: target.name }));
    close({ focusTarget: true });
  }

  function pick(id) {
    const e = loadHistory().find((x) => x.id === id) || loadFavourites().find((x) => x.id === id);
    if (!e) return;
    setEditor(e.expression, e.variables || varsEl.value);
    setOut("", "");
    exprEl.focus();
  }

  // ---------- wiring ----------

  sendBtn.addEventListener("click", send);
  askEl.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); send(); }
  });
  $("[data-run]").addEventListener("click", run);
  $("[data-copy]").addEventListener("click", doCopy);
  if (applyBtn) applyBtn.addEventListener("click", doApply);
  starBtn.addEventListener("click", () => {
    const note = [...session.messages].reverse().find((m) => m.role === "user");
    toggleFavourite({ expression: current(), variables: varsEl.value, name: note ? note.content : "" });
    paintStar(); paintLists();
  });
  exprEl.addEventListener("input", persist);
  varsEl.addEventListener("input", persist);
  exprEl.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); run(); }
  });
  $("[data-new]").addEventListener("click", () => {
    if (current()) remember({ expression: current(), variables: varsEl.value });
    session.messages = [];
    setStatus("", "");
    saveSession();
    paintLog(); paintLists();
    askEl.focus();
  });
  logEl.addEventListener("click", (e) => {
    const b = e.target.closest("[data-load]");
    if (!b) return;
    const m = session.messages[Number(b.dataset.load)];
    const v = m && m.view;
    if (v && v.expression) setEditor(v.expression, v.variables ? JSON.stringify(v.variables, null, 2) : varsEl.value);
  });
  ov.querySelector(".fa-tabs").addEventListener("click", (e) => {
    const b = e.target.closest("[data-tab]");
    if (!b) return;
    session.tab = b.dataset.tab;
    saveSession();
    paintLists();
  });
  listEl.addEventListener("click", (e) => {
    const p = e.target.closest("[data-pick]");
    if (p) { pick(p.dataset.pick); return; }
    const r = e.target.closest("[data-remove]");
    if (r) {
      if (session.tab === "favourites") removeFavourite(r.dataset.remove);
      else removeHistory(r.dataset.remove);
      paintLists(); paintStar();
      return;
    }
    const n = e.target.closest("[data-rename]");
    if (n) {
      const li = n.closest("li");
      const entry = loadFavourites().find((f) => f.id === n.dataset.rename);
      if (!entry || li.querySelector("input")) return;
      const input = document.createElement("input");
      input.className = "fa-rename";
      input.value = entry.name;
      input.setAttribute("aria-label", t("feel.list.rename"));
      li.querySelector(".fa-pick-name").replaceWith(input);
      input.focus(); input.select();
      const done = (keep) => { if (keep) renameFavourite(entry.id, input.value); paintLists(); };
      input.addEventListener("keydown", (k) => {
        if (k.key === "Enter") { k.preventDefault(); done(true); }
        if (k.key === "Escape") { k.preventDefault(); k.stopPropagation(); done(false); }
      });
      input.addEventListener("blur", () => done(true));
      return;
    }
    if (e.target.closest("[data-clear]")) { clearHistory(); paintLists(); }
  });
  $("[data-close]").addEventListener("click", () => close());
  ov.addEventListener("mousedown", (e) => { if (e.target === ov) close(); });
  ov.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !e.defaultPrevented) { e.preventDefault(); close(); }
  });

  function close(o = {}) {
    if (!isLive()) return;
    persist();
    try { editor.destroy(); } catch { /* the field is going with the overlay */ }
    ov.remove();
    live = null;
    const back = o.focusTarget && target ? target.el : returnTo;
    if (back && back.isConnected && back.focus) { try { back.focus(); } catch { /* not focusable any more */ } }
  }

  Object.assign(self, {
    close,
    focus() { (workers.length ? askEl : exprEl).focus(); },
    el: ov,
  });
  live = self;

  paintStar(); paintLog(); paintChatState(); paintLists();
  (workers.length ? askEl : exprEl).focus();
  if (api) {
    workersOf(api).then((list) => {
      workers = list;
      probed = true;
      if (!isLive()) return;
      paintWorker(); paintLog(); paintChatState();
      if (workers.length && !target) askEl.focus();
    });
  }
  return live;
}

// ---------- installing it ----------

// isFeelAssistantShortcut is Ctrl+Shift+E, or ⌘+Shift+E. The physical key is read, so
// the shortcut is the same key on a Swiss, a German and a US keyboard.
export function isFeelAssistantShortcut(e) {
  return (e.ctrlKey || e.metaKey) && e.shiftKey && !e.altKey &&
    (e.code === "KeyE" || String(e.key || "").toLowerCase() === "e");
}

let installed = false;

// installFeelAssistant makes the assistant reachable from everywhere in the console: the
// shortcut, the top-bar button, the mini spark on every FEEL field, and a floating spark
// beside a focused dmn-js cell. allowed() is asked at the moment of opening, because the
// signed-in principal is known only after boot (the routes need the modeler role).
export function installFeelAssistant({ api, copy, toast, allowed = () => true, button = null } = {}) {
  if (installed) return;
  installed = true;
  offerFeelAssistant(true);
  const openFrom = (el) => {
    if (!allowed()) return;
    if (live) { live.close(); return; }
    openFeelAssistant({ api, copy, toast, target: feelTarget(el) });
  };

  // Capture phase: the code editor, bpmn-js and dmn-js all bind keys of their own, and
  // this one must reach the assistant whichever of them has the focus.
  document.addEventListener("keydown", (e) => {
    if (!isFeelAssistantShortcut(e)) return;
    e.preventDefault();
    e.stopPropagation();
    openFrom(document.activeElement);
  }, true);

  document.addEventListener(FEEL_ASSISTANT_EVENT, (e) => openFrom(e.target));

  if (button) {
    button.innerHTML = SPARK_SVG;
    button.title = t("feel.open");
    button.setAttribute("aria-label", t("feel.open"));
    // mousedown, not click: the field the author was in keeps the focus, so the
    // assistant can still tell which one it was.
    button.addEventListener("mousedown", (e) => e.preventDefault());
    button.addEventListener("click", () => openFrom(document.activeElement));
  }

  installCellSpark(openFrom);
}

// installCellSpark shows one floating spark beside a focused dmn-js cell or editor that
// takes an expression. dmn-js renders and re-renders its cells itself, so a button
// placed inside one would be thrown away on the next keystroke; a single floating one,
// positioned at the focused cell, survives every re-render.
function installCellSpark(openFrom) {
  const spark = document.createElement("button");
  spark.type = "button";
  spark.className = "feel-ai-float";
  spark.hidden = true;
  spark.innerHTML = SPARK_SVG;
  spark.title = t("feel.open");
  spark.setAttribute("aria-label", t("feel.open"));
  document.body.appendChild(spark);
  let anchor = null;
  let hideTimer = 0;

  spark.addEventListener("mousedown", (e) => {
    e.preventDefault();
    if (anchor) openFrom(anchor);
  });
  document.addEventListener("focusin", (e) => {
    const target = feelTarget(e.target);
    if (!target || target.kind === "field" || !target.applicable || live) return;
    clearTimeout(hideTimer);
    anchor = target.el;
    const box = (anchor.closest("td") || anchor).getBoundingClientRect();
    spark.style.top = `${Math.max(0, box.top + 2)}px`;
    spark.style.left = `${Math.max(0, box.right - 22)}px`;
    spark.hidden = false;
  });
  document.addEventListener("focusout", () => {
    clearTimeout(hideTimer);
    hideTimer = setTimeout(() => { spark.hidden = true; anchor = null; }, 150);
  });
  // A spark left behind by a scroll would point at the wrong cell.
  window.addEventListener("scroll", () => { spark.hidden = true; }, true);
}
