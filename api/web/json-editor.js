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

  // Toolbar: format button.
  const toolbar = document.createElement("div");
  toolbar.className = "json-toolbar";
  toolbar.innerHTML = `<button type="button" class="json-fmt icon-btn" title="Format JSON" aria-label="Format JSON">{ }</button>`;

  wrap.appendChild(pre);
  wrap.appendChild(textarea);
  wrap.appendChild(toolbar);
  textarea.classList.add("json-input");
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
      wrap.parentNode.insertBefore(textarea, wrap);
      wrap.remove();
    },
    getValue() { return textarea.value; },
    setValue(v) {
      textarea.value = typeof v === "string" ? v : JSON.stringify(v, null, 2);
      renderHighlight();
      validate();
    },
  };
}
