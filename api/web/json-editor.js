// A buildless, self-contained JSON editor surface for the Modeler's structured
// variable fields. It follows the same overlay technique as feel.js — a
// transparent <textarea> over a highlighted <pre> — and is designed to feel at
// home in the Atlas design system (ADR-0012, ADR-0013).
//
// Features:
//   • JSON syntax colouring (strings, numbers, booleans/null, keys, brackets)
//   • Live validation with a red border + error message
//   • Auto-indent on Enter (matches the current nesting depth)
//   • Auto-close brackets, braces, and quotes
//   • Format (pretty-print) button
//   • Compact mode for inline defaults (single-row, expands on focus)

// ---------- Tokenizer ----------

// tokenizeJSON breaks a JSON string into typed spans for highlighting.
export function tokenizeJSON(src) {
  const out = [];
  const n = src.length;
  let i = 0;
  const push = (type, value) => out.push({ type, value });

  while (i < n) {
    const ch = src[i];

    // Whitespace.
    if (/\s/.test(ch)) {
      let j = i + 1;
      while (j < n && /\s/.test(src[j])) j++;
      push("ws", src.slice(i, j));
      i = j;
      continue;
    }

    // Strings (JSON double-quoted with backslash escapes).
    if (ch === '"') {
      let j = i + 1;
      while (j < n) {
        if (src[j] === "\\") { j += 2; continue; }
        if (src[j] === '"') { j++; break; }
        j++;
      }
      const text = src.slice(i, j);
      // Peek ahead past whitespace for a colon — that makes this a key.
      let k = j;
      while (k < n && /\s/.test(src[k])) k++;
      push(src[k] === ":" ? "key" : "string", text);
      i = j;
      continue;
    }

    // Numbers (incl. negative, decimal, exponent).
    if (/[-0-9]/.test(ch) && (ch !== "-" || /[0-9]/.test(src[i + 1] || ""))) {
      let j = i;
      if (src[j] === "-") j++;
      while (j < n && /[0-9]/.test(src[j])) j++;
      if (src[j] === ".") { j++; while (j < n && /[0-9]/.test(src[j])) j++; }
      if (src[j] === "e" || src[j] === "E") {
        j++;
        if (src[j] === "+" || src[j] === "-") j++;
        while (j < n && /[0-9]/.test(src[j])) j++;
      }
      push("number", src.slice(i, j));
      i = j;
      continue;
    }

    // Keywords: true, false, null.
    if (/[tfn]/.test(ch)) {
      for (const kw of ["true", "false", "null"]) {
        if (src.startsWith(kw, i) && !/[a-zA-Z0-9_]/.test(src[i + kw.length] || "")) {
          push("literal", kw);
          i += kw.length;
          break;
        }
      }
      if (out.length && out[out.length - 1].type === "literal") continue;
      // Not a keyword — consume as unknown identifier.
      let j = i + 1;
      while (j < n && /[a-zA-Z_]/.test(src[j])) j++;
      push("error", src.slice(i, j));
      i = j;
      continue;
    }

    // Structural characters.
    if ("{}[]:,".includes(ch)) {
      push("punct", ch);
      i++;
      continue;
    }

    // Anything else is an error token (highlights red).
    push("error", ch);
    i++;
  }
  return out;
}

const escapeHTML = (s) => s.replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));

// highlightJSON renders a JSON string as coloured HTML spans for the backdrop.
export function highlightJSON(src) {
  let html = "";
  for (const tok of tokenizeJSON(src)) {
    const text = escapeHTML(tok.value);
    if (tok.type === "ws") { html += text; continue; }
    html += `<span class="jtok-${tok.type}">${text}</span>`;
  }
  return html;
}

// ---------- Editor ----------

// attachJSONEditor upgrades a <textarea> into a JSON editor in place, mirroring
// the feel.js technique: transparent textarea over a highlighted <pre>. Returns
// a handle: { destroy(), getValue(), setValue(v) }.
//
// opts.compact  — start in single-line mode, expand on focus (for inline defaults)
// opts.onChange — called when the value changes (debounced for typing)
// opts.rows    — initial rows for the textarea (default 3)
export function attachJSONEditor(textarea, opts = {}) {
  if (!textarea || textarea.dataset.jsonOn === "1") return null;
  textarea.dataset.jsonOn = "1";

  const wrap = document.createElement("div");
  wrap.className = "json-editor" + (opts.compact ? " compact" : "");
  textarea.parentNode.insertBefore(wrap, textarea);

  const pre = document.createElement("pre");
  pre.className = "json-highlight";
  pre.setAttribute("aria-hidden", "true");
  const code = document.createElement("code");
  pre.appendChild(code);

  // Toolbar: format button, plus (compact/inline fields) an expand button that opens
  // the value in a large modal editor — the comfortable surface for a nested array
  // that a 34px inline box can't show.
  const toolbar = document.createElement("div");
  toolbar.className = "json-toolbar";
  toolbar.innerHTML = `<button type="button" class="json-fmt icon-btn" title="Format JSON" aria-label="Format JSON">{ }</button>` +
    (opts.compact ? `<button type="button" class="json-expand icon-btn" title="Open in a large editor" aria-label="Open in a large editor">⤢</button>` : "");

  wrap.appendChild(pre);
  wrap.appendChild(textarea);
  wrap.appendChild(toolbar);
  textarea.classList.add("json-input");
  // A JSON value is a code field: F2 opens it in the Developer View (ADR-0145).
  // Declared by attribute rather than by importing dev-view.js, which would close a
  // cycle — dev-lang.js reads this module's tokenizer.
  textarea.dataset.devlang = "json";
  textarea.setAttribute("spellcheck", "false");
  textarea.setAttribute("autocapitalize", "off");
  textarea.setAttribute("autocomplete", "off");
  if (opts.rows && !opts.compact) textarea.rows = opts.rows;

  // Validation status line.
  const statusEl = document.createElement("div");
  statusEl.className = "json-status";
  statusEl.setAttribute("role", "alert");
  statusEl.hidden = true;
  wrap.after(statusEl);

  let destroyed = false;
  let validateTimer = null;

  function renderHighlight() {
    code.innerHTML = highlightJSON(textarea.value) + "​";
    pre.scrollTop = textarea.scrollTop;
    pre.scrollLeft = textarea.scrollLeft;
  }

  function showValid() {
    wrap.classList.remove("invalid");
    statusEl.hidden = true;
    statusEl.textContent = "";
  }

  function showInvalid(msg) {
    wrap.classList.add("invalid");
    statusEl.hidden = false;
    statusEl.textContent = msg;
  }

  function validate() {
    const v = textarea.value.trim();
    if (v === "") { showValid(); return; }
    try {
      JSON.parse(v);
      showValid();
    } catch (e) {
      showInvalid(e.message.replace(/^JSON\.parse: /, ""));
    }
  }

  function scheduleValidate() {
    clearTimeout(validateTimer);
    validateTimer = setTimeout(validate, 300);
  }

  function format() {
    const v = textarea.value.trim();
    if (!v) return;
    try {
      const obj = JSON.parse(v);
      textarea.value = JSON.stringify(obj, null, 2);
      renderHighlight();
      validate();
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      textarea.dispatchEvent(new Event("change", { bubbles: true }));
    } catch { /* leave as-is if invalid */ }
  }

  // Auto-indent: on Enter, insert a newline plus the current nesting depth.
  function handleEnter(e) {
    if (e.key !== "Enter" || e.ctrlKey || e.metaKey) return false;
    e.preventDefault();
    const { selectionStart: s, value } = textarea;
    const before = value.slice(0, s);
    const after = value.slice(s);

    // Count nesting depth by open/close brackets up to the cursor.
    let depth = 0;
    for (const ch of before) {
      if (ch === "{" || ch === "[") depth++;
      if (ch === "}" || ch === "]") depth--;
    }
    depth = Math.max(0, depth);

    // If the character before the cursor is an opener and the one after is the
    // matching closer, split them and indent the cursor one level deeper.
    const charBefore = before.slice(-1);
    const charAfter = after[0];
    const isOpenClose =
      (charBefore === "{" && charAfter === "}") ||
      (charBefore === "[" && charAfter === "]");

    const indent = "  ".repeat(depth);
    if (isOpenClose) {
      const inner = "\n" + indent;
      const outer = "\n" + "  ".repeat(depth - 1);
      textarea.setRangeText(inner + outer, s, s, "end");
      textarea.setSelectionRange(s + inner.length, s + inner.length);
    } else {
      const nl = "\n" + indent;
      textarea.setRangeText(nl, s, s, "end");
      textarea.setSelectionRange(s + nl.length, s + nl.length);
    }
    afterEdit();
    return true;
  }

  // Auto-close brackets/braces/quotes.
  const OPENERS = { "{": "}", "[": "]", '"': '"' };
  const CLOSERS = new Set(["}", "]", '"']);

  function handleBracket(e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return false;
    const { selectionStart: s, selectionEnd: eSel, value } = textarea;
    // Skip over an auto-inserted closer.
    if (s === eSel && CLOSERS.has(e.key) && value[s] === e.key) {
      e.preventDefault();
      textarea.setSelectionRange(s + 1, s + 1);
      return true;
    }
    if (OPENERS[e.key]) {
      e.preventDefault();
      const sel = value.slice(s, eSel);
      textarea.setRangeText(e.key + sel + OPENERS[e.key], s, eSel, "end");
      const pos = s + 1 + sel.length;
      textarea.setSelectionRange(sel ? s + 1 : pos, sel ? eSel + 1 : pos);
      afterEdit();
      return true;
    }
    return false;
  }

  // Tab inserts two spaces (no focus change).
  function handleTab(e) {
    if (e.key !== "Tab" || e.ctrlKey || e.metaKey) return false;
    e.preventDefault();
    const { selectionStart: s } = textarea;
    textarea.setRangeText("  ", s, s, "end");
    textarea.setSelectionRange(s + 2, s + 2);
    afterEdit();
    return true;
  }

  function afterEdit() {
    renderHighlight();
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  }

  // ---- event wiring ----
  const onInput = () => { renderHighlight(); scheduleValidate(); if (opts.onChange) opts.onChange(textarea.value); };
  const onScroll = () => { pre.scrollTop = textarea.scrollTop; pre.scrollLeft = textarea.scrollLeft; };

  function onKeydown(e) {
    if (handleEnter(e)) return;
    if (handleTab(e)) return;
    if (handleBracket(e)) return;
  }

  textarea.addEventListener("input", onInput);
  textarea.addEventListener("scroll", onScroll);
  textarea.addEventListener("keydown", onKeydown);
  toolbar.querySelector(".json-fmt").addEventListener("click", format);

  // Expand-to-modal: edit the value in a full-size JSON editor (a non-compact one,
  // so it carries no further expand button), writing back on Apply. The overlay is
  // tracked so destroying this editor (e.g. a removed row) tears it down too.
  let modalOverlay = null;
  const closeModal = () => {
    if (!modalOverlay) return;
    try { modalOverlay._handle && modalOverlay._handle.destroy(); } catch { /* gone */ }
    document.removeEventListener("keydown", modalOverlay._onKey, true);
    modalOverlay.remove();
    modalOverlay = null;
  };
  const openModal = () => {
    if (modalOverlay || destroyed) return;
    const overlay = document.createElement("div");
    overlay.className = "json-modal-overlay";
    overlay.innerHTML = `
      <div class="json-modal" role="dialog" aria-modal="true" aria-label="Edit JSON value">
        <div class="json-modal-head">
          <strong>Edit JSON</strong>
          <span style="flex:1"></span>
          <button type="button" class="btn ghost small json-modal-fmt" title="Reformat the JSON">{ } Format</button>
          <button type="button" class="btn ghost small json-modal-cancel" title="Close without applying changes">Cancel</button>
          <button type="button" class="btn small json-modal-apply" title="Apply the edited JSON">Apply</button>
        </div>
        <div class="json-modal-body"><textarea class="json-modal-ta" spellcheck="false" aria-label="JSON value"></textarea></div>
      </div>`;
    document.body.appendChild(overlay);
    modalOverlay = overlay;
    const ta2 = overlay.querySelector(".json-modal-ta");
    ta2.value = textarea.value;
    const handle = attachJSONEditor(ta2, { rows: 18 });
    overlay._handle = handle;
    ta2.focus();
    const apply = () => {
      textarea.value = handle ? handle.getValue() : ta2.value;
      renderHighlight();
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      textarea.dispatchEvent(new Event("change", { bubbles: true }));
      closeModal();
    };
    overlay.querySelector(".json-modal-apply").addEventListener("click", apply);
    overlay.querySelector(".json-modal-cancel").addEventListener("click", closeModal);
    overlay.querySelector(".json-modal-fmt").addEventListener("click", () => { if (handle) handle.format(); });
    overlay.addEventListener("mousedown", (e) => { if (e.target === overlay) closeModal(); });
    overlay._onKey = (e) => { if (e.key === "Escape") { e.stopPropagation(); closeModal(); } };
    document.addEventListener("keydown", overlay._onKey, true);
  };
  const expandBtn = toolbar.querySelector(".json-expand");
  if (expandBtn) expandBtn.addEventListener("click", openModal);

  // Compact mode: expand on focus, collapse on blur.
  if (opts.compact) {
    textarea.addEventListener("focus", () => wrap.classList.add("expanded"));
    textarea.addEventListener("blur", () => {
      if (textarea.value.trim() === "") wrap.classList.remove("expanded");
    });
  }

  renderHighlight();
  validate();

  return {
    destroy() {
      destroyed = true;
      clearTimeout(validateTimer);
      textarea.removeEventListener("input", onInput);
      textarea.removeEventListener("scroll", onScroll);
      textarea.removeEventListener("keydown", onKeydown);
      textarea.classList.remove("json-input");
      delete textarea.dataset.jsonOn;
      statusEl.remove();
      closeModal();
      wrap.parentNode.insertBefore(textarea, wrap);
      wrap.remove();
    },
    format,
    getValue() { return textarea.value; },
    setValue(v) {
      textarea.value = typeof v === "string" ? v : JSON.stringify(v, null, 2);
      renderHighlight();
      validate();
    },
  };
}
