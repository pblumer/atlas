// A small, buildless code-editing surface for the Modeler's script fields.
//
// The Implement tab lets a task carry a general-purpose script (PowerShell,
// Python, JavaScript — ADR-0047). A plain <textarea> makes that field feel like a
// note pad, not a place a developer wants to write code. This module turns it into
// a lightweight editor — syntax colouring, context-aware completion, a line-number
// gutter, error markers, bracket/quote helpers and code-style Tab handling —
// without a framework, a bundler or a CDN, honouring the buildless self-contained
// rules of ADR-0012/ADR-0013.
//
// It is the sibling of feel.js: feel.js pioneered the transparent-textarea-over-a-
// highlighted-<pre> technique for FEEL; this generalises that same technique to any
// language via a pluggable *language module* so PowerShell, Python and JavaScript
// share one widget. (feel.js is intentionally left on its own code path for now —
// it is heavily used and has no JS test net; folding it onto this widget is a
// deliberate later step.) The chrome reuses feel.js's popup/tooltip CSS classes so
// the two editors look like one product.
//
// A language module is a plain object of pure functions:
//   tokenize(src)            -> [{ type, value, name?, tip? }]   spans covering all
//                               of src (join(values) === src); `ws` is whitespace.
//   completions(prefix, ctx) -> [{ label, kind, insert?, detail?, doc?, call? }]
//                               ranked items; ctx = { variables, source, before }.
//   prefix(before)           -> { text, start } | null           what the caret is
//                               completing; defaults to a trailing identifier word.
//   tooltip(name)            -> { sig, doc } | null               for a hovered token
//                               carrying tip:true and a name.
// See powershell.js for a full module.

const escapeHTML = (s) => s.replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));

// The default completion prefix: the identifier word immediately before the caret.
// A language whose tokens include sigils/dashes (PowerShell's `$env:X`, `-Param`)
// supplies its own lang.prefix.
function defaultPrefix(before) {
  const m = /[A-Za-z_][A-Za-z0-9_]*$/.exec(before);
  return m ? { text: m[0], start: before.length - m[0].length } : { text: "", start: before.length };
}

const KIND_LABEL = {
  variable: "var", function: "fn", keyword: "kw", literal: "lit",
  cmdlet: "cmd", parameter: "arg", operator: "op", type: "type", env: "env", member: "mem",
};

// highlightTokens renders a token list as backdrop HTML. A token that opts into
// tooltips (tip:true with a name) is tagged with data-fn so the hover hit-test can
// find it; an identifier that names a known variable is recoloured as one.
// normVars accepts the caller's variables as either bare name strings or
// { name, detail } objects (detail is the origin label shown in completion), and
// returns both a Set of names (for highlighting an in-scope identifier) and the
// labelled list (for the completion popup).
function normVars(list) {
  const names = new Set();
  const arr = [];
  for (const v of list || []) {
    const name = typeof v === "string" ? v : (v && v.name);
    if (!name) continue;
    names.add(name);
    arr.push(typeof v === "string" ? { name, detail: "variable" } : { name, detail: v.detail || "variable" });
  }
  return { names, arr };
}

function highlightTokens(tokens, variables) {
  let html = "";
  for (const t of tokens) {
    const text = escapeHTML(t.value);
    if (t.type === "ws") { html += text; continue; }
    let cls = t.type;
    if (t.type === "name" && variables.has(t.value)) cls = "variable";
    if (t.tip && t.name) {
      html += `<span class="tok-${cls}" data-fn="${escapeHTML(t.name)}">${text}</span>`;
    } else {
      html += `<span class="tok-${cls}">${text}</span>`;
    }
  }
  return html;
}

// attachCodeEditor upgrades an existing <textarea> into a code editor in place and
// returns a handle:
//   destroy()          — remove the chrome, leaving the bare textarea.
//   setVariables(list) — update the in-scope variable names (colour + completion).
//   setMarkers(list)   — show error markers; each { line (1-based), message }.
//   focusLine(n)       — scroll a line into view (used after a run error).
// opts: { lang, variables, validate, gutter, wrap, hint }.
export function attachCodeEditor(textarea, opts = {}) {
  if (!textarea || textarea.dataset.ceOn === "1") return null;
  const lang = opts.lang;
  if (!lang || typeof lang.tokenize !== "function") return null;
  textarea.dataset.ceOn = "1";

  let { names: varNames, arr: varList } = normVars(opts.variables);
  const gutterOn = opts.gutter !== false;
  const wrap = opts.wrap === true; // scripts default to no-wrap (horizontal scroll)

  // A locked prefix is a leading run the editor shows dimmed and protects — the FEEL
  // '=' expression marker (ADR-0067, the fx toggle). It cannot be edited or deleted,
  // the caret cannot enter it, and it is stripped before validation (the parser wants
  // the bare expression, not the storage form). opts.lockPrefix(value) returns the
  // protected length for the current value (0 = none). No-op for every other field.
  const lockPrefix = typeof opts.lockPrefix === "function" ? opts.lockPrefix : null;
  const lockLen = () => {
    if (!lockPrefix) return 0;
    const v = textarea.value;
    return Math.max(0, Math.min(lockPrefix(v) | 0, v.length));
  };

  // --- chrome ---
  const wrapEl = document.createElement("div");
  wrapEl.className = "code-editor" + (wrap ? "" : " nowrap") + (gutterOn ? " has-gutter" : "");
  textarea.parentNode.insertBefore(wrapEl, textarea);

  const pre = document.createElement("pre");
  pre.className = "ce-highlight";
  pre.setAttribute("aria-hidden", "true");
  const code = document.createElement("code");
  pre.appendChild(code);

  const markLayer = document.createElement("div");
  markLayer.className = "ce-markers";
  markLayer.setAttribute("aria-hidden", "true");

  const gutter = document.createElement("div");
  gutter.className = "ce-gutter";
  gutter.setAttribute("aria-hidden", "true");

  const mirror = document.createElement("div");
  mirror.className = "ce-highlight ce-mirror";
  mirror.setAttribute("aria-hidden", "true");

  const pop = document.createElement("div");
  pop.className = "feel-pop"; // reuse feel.js popup styling
  pop.setAttribute("role", "listbox");
  pop.hidden = true;

  wrapEl.appendChild(pre);
  wrapEl.appendChild(markLayer);
  if (gutterOn) wrapEl.appendChild(gutter);
  wrapEl.appendChild(mirror);
  wrapEl.appendChild(textarea);
  wrapEl.appendChild(pop);
  textarea.classList.add("ce-input");
  textarea.setAttribute("spellcheck", "false");
  textarea.setAttribute("autocapitalize", "off");
  textarea.setAttribute("autocomplete", "off");
  textarea.setAttribute("autocorrect", "off");

  // Line metrics, read once from the resolved styles so the gutter and markers
  // align with the text exactly whatever the theme's font settings are.
  const cs = getComputedStyle(textarea);
  const lineH = parseFloat(cs.lineHeight) || 19;
  const padTop = parseFloat(cs.paddingTop) || 8;

  let items = [];
  let active = -1;
  let markers = [];

  function lineCount() {
    let n = 1;
    const v = textarea.value;
    for (let i = 0; i < v.length; i++) if (v[i] === "\n") n++;
    return n;
  }

  function renderGutter() {
    if (!gutterOn) return;
    const n = lineCount();
    const digits = String(n).length;
    wrapEl.style.setProperty("--ce-gutter", digits + "ch");
    let html = "";
    for (let i = 1; i <= n; i++) {
      const m = markers.find((mk) => mk.line === i);
      html += `<div class="ce-ln${m ? " err" : ""}"${m ? ` title="${escapeHTML(m.message || "error")}"` : ""}>${i}</div>`;
    }
    gutter.innerHTML = html;
    gutter.scrollTop = textarea.scrollTop;
  }

  function renderMarkers() {
    if (!markers.length) { markLayer.innerHTML = ""; return; }
    markLayer.innerHTML = markers.map((m) => {
      const top = padTop + (m.line - 1) * lineH - textarea.scrollTop;
      return `<div class="ce-marker err" style="top:${top}px;height:${lineH}px"></div>`;
    }).join("");
  }

  function renderHighlight() {
    // A trailing newline is not rendered by <pre> unless followed by content;
    // a zero-width space keeps the last line's height and the caret aligned.
    // A locked prefix is rendered verbatim in its own dimmed span and excluded from
    // tokenisation; the two segments still cover every character in order, so the
    // transparent textarea on top stays aligned character-for-character.
    const v = textarea.value;
    const lp = lockLen();
    const head = lp ? `<span class="ce-locked">${escapeHTML(v.slice(0, lp))}</span>` : "";
    code.innerHTML = head + highlightTokens(lang.tokenize(v.slice(lp)), varNames) + "​";
    pre.scrollTop = textarea.scrollTop;
    pre.scrollLeft = textarea.scrollLeft;
    renderGutter();
    renderMarkers();
  }

  // caretRect returns the caret's {top,left} within the wrapper, so the popup can
  // be anchored just below it. The mirror shares the input's box metrics.
  function caretRect() {
    mirror.style.width = textarea.clientWidth + "px";
    const before = textarea.value.slice(0, textarea.selectionStart);
    mirror.textContent = before;
    const marker = document.createElement("span");
    marker.textContent = "​";
    mirror.appendChild(marker);
    const top = marker.offsetTop - textarea.scrollTop;
    const left = marker.offsetLeft - textarea.scrollLeft;
    const lh = marker.offsetHeight || lineH;
    mirror.textContent = "";
    return { top, left, lineHeight: lh };
  }

  function closePopup() { pop.hidden = true; pop.innerHTML = ""; items = []; active = -1; }

  function renderPopup() {
    if (!items.length) { closePopup(); return; }
    pop.innerHTML = items.map((it, idx) => `
      <div class="feel-opt${idx === active ? " active" : ""}" role="option" data-idx="${idx}"
           aria-selected="${idx === active}">
        <span class="feel-opt-kind k-${it.kind}">${KIND_LABEL[it.kind] || ""}</span>
        <span class="feel-opt-label">${escapeHTML(it.label)}</span>
        <span class="feel-opt-detail">${escapeHTML(it.detail || "")}</span>
      </div>`).join("");
    const r = caretRect();
    const maxLeft = Math.max(0, wrapEl.clientWidth - 240);
    pop.style.left = Math.min(r.left, maxLeft) + "px";
    pop.style.top = r.top + r.lineHeight + 2 + "px";
    pop.hidden = false;
  }

  function currentPrefix() {
    const before = textarea.value.slice(0, textarea.selectionStart);
    const p = (lang.prefix ? lang.prefix(before) : defaultPrefix(before)) || defaultPrefix(before);
    return { before, ...p };
  }

  function openCompletion(explicit) {
    const { before, text } = currentPrefix();
    if (!explicit && text === "") { closePopup(); return; }
    const ctx = { variables: varList, source: textarea.value, before };
    items = (lang.completions ? lang.completions(text, ctx) : []).slice(0, 50);
    active = items.length ? 0 : -1;
    renderPopup();
  }

  function accept(idx) {
    const it = items[idx];
    if (!it) return;
    const caret = textarea.selectionStart;
    // A language may overwrite more than the caret word — FEEL's multi-word
    // builtins ("string length") replace the whole already-typed run.
    const before = textarea.value.slice(0, caret);
    const start = lang.replaceStart ? lang.replaceStart(before, it) : currentPrefix().start;
    let text = it.insert != null ? it.insert : it.label;
    let caretDelta = text.length;
    if (it.call) { text = text + "()"; caretDelta = text.length - 1; }
    textarea.setRangeText(text, start, caret, "end");
    const pos = start + caretDelta;
    textarea.setSelectionRange(pos, pos);
    closePopup();
    afterEdit();
    textarea.focus();
  }

  function moveActive(delta) {
    if (!items.length) return;
    active = (active + delta + items.length) % items.length;
    for (const el of pop.querySelectorAll(".feel-opt")) {
      const on = Number(el.dataset.idx) === active;
      el.classList.toggle("active", on);
      el.setAttribute("aria-selected", on ? "true" : "false");
      if (on) el.scrollIntoView({ block: "nearest" });
    }
  }

  function afterEdit() {
    renderHighlight();
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  }

  // --- bracket / quote helpers ---
  const OPENERS = { "(": ")", "[": "]", "{": "}", '"': '"', "'": "'" };
  const CLOSERS = new Set([")", "]", "}", '"', "'"]);
  function handleBracket(e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return false;
    const { selectionStart: s, selectionEnd: eSel, value } = textarea;
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

  // --- code-style Tab / Enter ---
  const INDENT = "  "; // two spaces, matching tab-size
  const lineStart = (v, pos) => v.lastIndexOf("\n", pos - 1) + 1;
  const leadingWS = (line) => (/^[ \t]*/.exec(line) || [""])[0];

  function indentRange(dedent) {
    const { selectionStart: s, selectionEnd: e2, value } = textarea;
    const from = lineStart(value, s);
    const to = e2 > s && value[e2 - 1] === "\n" ? e2 - 1 : e2;
    const block = value.slice(from, to);
    const lines = block.split("\n");
    let delta = 0;
    const out = lines.map((ln) => {
      if (dedent) {
        const cut = /^( {1,2}|\t)/.exec(ln);
        if (cut) { delta -= cut[0].length; return ln.slice(cut[0].length); }
        return ln;
      }
      delta += INDENT.length;
      return INDENT + ln;
    }).join("\n");
    textarea.setRangeText(out, from, to, "preserve");
    // Keep the selection roughly over the same text.
    textarea.setSelectionRange(from, to + delta);
    afterEdit();
  }

  function handleTab(e) {
    const { selectionStart: s, selectionEnd: eSel, value } = textarea;
    const multiline = value.slice(s, eSel).includes("\n");
    if (e.shiftKey) { e.preventDefault(); indentRange(true); return true; }
    if (multiline) { e.preventDefault(); indentRange(false); return true; }
    e.preventDefault();
    textarea.setRangeText(INDENT, s, eSel, "end");
    afterEdit();
    return true;
  }

  function handleEnter(e) {
    if (e.shiftKey) return false;
    const { selectionStart: s, value } = textarea;
    const line = value.slice(lineStart(value, s), s);
    let indent = leadingWS(line);
    const prevChar = value[s - 1];
    // Opening a block indents one level deeper; closing it on a fresh } outdents.
    if (prevChar === "{" || prevChar === "(" || prevChar === "[" || /\{\s*$/.test(line)) {
      indent += INDENT;
    }
    e.preventDefault();
    textarea.setRangeText("\n" + indent, s, s, "end");
    afterEdit();
    return true;
  }

  // --- live validation (optional) ---
  const validate = typeof opts.validate === "function" ? opts.validate : null;
  let statusEl = null;
  let validateTimer = null;
  let validateSeq = 0;
  let destroyed = false;
  if (validate) {
    statusEl = document.createElement("div");
    statusEl.className = "feel-status";
    statusEl.setAttribute("role", "alert");
    statusEl.hidden = true;
    wrapEl.after(statusEl);
  }
  function showValid() {
    wrapEl.classList.remove("invalid");
    if (statusEl) { statusEl.hidden = true; statusEl.textContent = ""; }
  }
  function showInvalid(msg) {
    wrapEl.classList.add("invalid");
    if (statusEl) { statusEl.hidden = false; statusEl.textContent = msg; }
  }
  async function runValidate() {
    if (!validate) return;
    // Validate the bare expression: the locked '=' marker is the storage form, not
    // part of what the FEEL parser accepts (a leading '=' is "unexpected").
    const src = textarea.value.slice(lockLen());
    const seq = ++validateSeq;
    if (src.trim() === "") { showValid(); return; }
    let res;
    try { res = await validate(src); } catch { return; }
    if (destroyed || seq !== validateSeq) return;
    if (res && res.ok === false) showInvalid(res.error || "invalid");
    else showValid();
  }
  function scheduleValidate() {
    if (!validate) return;
    clearTimeout(validateTimer);
    validateTimer = setTimeout(runValidate, 400);
  }

  // --- hover tooltips (reuse feel.js tooltip look) ---
  const tip = document.createElement("div");
  tip.className = "feel-tip";
  tip.hidden = true;
  wrapEl.appendChild(tip);
  let tipFn = null;
  let hoverRaf = 0;
  const hideTip = () => { tip.hidden = true; tipFn = null; };
  function updateTip(x, y) {
    if (!lang.tooltip) { hideTip(); return; }
    const span = document.elementsFromPoint(x, y).find((el) => el.dataset && el.dataset.fn);
    const info = span ? lang.tooltip(span.dataset.fn) : null;
    if (!info) { hideTip(); return; }
    if (span.dataset.fn !== tipFn) {
      tip.innerHTML = `<div class="feel-tip-sig">${escapeHTML(info.sig)}</div>` +
        (info.doc ? `<div class="feel-tip-doc">${escapeHTML(info.doc)}</div>` : "");
      tipFn = span.dataset.fn;
    }
    const r = span.getBoundingClientRect();
    const wr = wrapEl.getBoundingClientRect();
    const maxLeft = Math.max(0, wrapEl.clientWidth - 244);
    tip.style.left = Math.min(Math.max(0, r.left - wr.left), maxLeft) + "px";
    tip.style.top = (r.bottom - wr.top + 4) + "px";
    tip.hidden = false;
  }
  const onHover = (e) => {
    if (hoverRaf) return;
    const x = e.clientX, y = e.clientY;
    hoverRaf = requestAnimationFrame(() => { hoverRaf = 0; updateTip(x, y); });
  };

  // --- events ---
  const onInput = () => { renderHighlight(); openCompletion(false); scheduleValidate(); hideTip(); };
  const onScroll = () => {
    pre.scrollTop = textarea.scrollTop; pre.scrollLeft = textarea.scrollLeft;
    if (gutterOn) gutter.scrollTop = textarea.scrollTop;
    renderMarkers();
  };
  const onBlur = () => { setTimeout(closePopup, 120); };

  // --- locked-prefix protection ---
  // Keep a collapsed caret out of the locked run (a click or Arrow-Left that lands
  // inside the dimmed '=' snaps back to just after it).
  function clampCaret() {
    const lp = lockLen();
    if (!lp) return;
    const { selectionStart: s, selectionEnd: e2 } = textarea;
    if (s === e2 && s < lp) textarea.setSelectionRange(lp, lp);
  }
  // Reject any edit that would reach into the locked run: an insertion or a
  // replacement bites from selectionStart; a backward delete also bites the character
  // left of a collapsed caret, so it's blocked when the caret sits at the boundary.
  function onBeforeInput(e) {
    const lp = lockLen();
    if (!lp) return;
    const s = textarea.selectionStart, e2 = textarea.selectionEnd;
    const t = e.inputType || "";
    let hit;
    if (s !== e2) hit = s < lp;
    else if (t.startsWith("delete") && /Backward/i.test(t)) hit = s <= lp;
    else if (t.startsWith("delete") && /Forward/i.test(t)) hit = s < lp;
    else hit = s < lp;
    if (!hit) return;
    e.preventDefault();
    if (s < lp) textarea.setSelectionRange(lp, Math.max(lp, e2)); // nudge the caret out
  }

  function onKeydown(e) {
    if (!pop.hidden) {
      if (e.key === "ArrowDown") { e.preventDefault(); moveActive(1); return; }
      if (e.key === "ArrowUp") { e.preventDefault(); moveActive(-1); return; }
      if (e.key === "Enter" || e.key === "Tab") {
        if (active >= 0) { e.preventDefault(); accept(active); return; }
      }
      if (e.key === "Escape") { e.preventDefault(); closePopup(); return; }
    }
    if ((e.ctrlKey || e.metaKey) && e.key === " ") { e.preventDefault(); openCompletion(true); return; }
    if (e.key === "Tab") { if (handleTab(e)) return; }
    if (e.key === "Enter") { if (handleEnter(e)) return; }
    if (handleBracket(e)) return;
  }

  pop.addEventListener("mousedown", (e) => {
    const opt = e.target.closest(".feel-opt");
    if (opt) { e.preventDefault(); accept(Number(opt.dataset.idx)); }
  });

  textarea.addEventListener("input", onInput);
  textarea.addEventListener("scroll", onScroll);
  textarea.addEventListener("keydown", onKeydown);
  textarea.addEventListener("blur", onBlur);
  textarea.addEventListener("mousemove", onHover);
  textarea.addEventListener("mouseleave", hideTip);
  if (lockPrefix) {
    textarea.addEventListener("beforeinput", onBeforeInput);
    textarea.addEventListener("keyup", clampCaret);
    textarea.addEventListener("click", clampCaret);
  }

  renderHighlight();
  runValidate();

  return {
    el: wrapEl,
    destroy() {
      destroyed = true;
      clearTimeout(validateTimer);
      if (hoverRaf) cancelAnimationFrame(hoverRaf);
      textarea.removeEventListener("input", onInput);
      textarea.removeEventListener("scroll", onScroll);
      textarea.removeEventListener("keydown", onKeydown);
      textarea.removeEventListener("blur", onBlur);
      textarea.removeEventListener("mousemove", onHover);
      textarea.removeEventListener("mouseleave", hideTip);
      if (lockPrefix) {
        textarea.removeEventListener("beforeinput", onBeforeInput);
        textarea.removeEventListener("keyup", clampCaret);
        textarea.removeEventListener("click", clampCaret);
      }
      textarea.classList.remove("ce-input");
      delete textarea.dataset.ceOn;
      if (statusEl) statusEl.remove();
      wrapEl.parentNode.insertBefore(textarea, wrapEl);
      wrapEl.remove();
    },
    setVariables(list) { const n = normVars(list); varNames = n.names; varList = n.arr; renderHighlight(); },
    setMarkers(list) {
      markers = (list || []).filter((m) => m && m.line > 0);
      renderGutter();
      renderMarkers();
    },
    focusLine(n) {
      const target = padTop + (n - 1) * lineH;
      textarea.scrollTop = Math.max(0, target - textarea.clientHeight / 2);
      onScroll();
    },
  };
}

// Exported for a future test runner and for reuse by language modules.
export { highlightTokens, defaultPrefix, escapeHTML };
