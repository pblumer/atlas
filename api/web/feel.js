// A small, buildless FEEL editing surface for the Modeler's expression fields.
//
// The Implement tab exposes several FEEL fields (a script task's expression, a
// gateway branch's condition, a message correlation key). Plain <textarea>s make
// those fields feel like note-taking, not programming. This module turns them
// into a lightweight code editor — syntax colouring plus context-aware
// completion — without a framework, a bundler, or a CDN, honouring the buildless
// self-contained rules of ADR-0012/ADR-0013.
//
// The technique is deliberately dependency-free: a transparent <textarea> sits
// over a highlighted <pre> that mirrors its text and scroll. The textarea is the
// single source of truth for the value, so the editor's existing change-to-save
// wiring keeps working unchanged — attachFeelEditor enhances the element in
// place, it does not replace it.
//
// The pure pieces (tokenize, highlight, completionsFor) carry no DOM state so
// they can be reasoned about — and, should a JS test runner ever land, tested —
// on their own.

// ---------- Language ----------

// Reserved words that are control flow / operators, not function calls. `in`,
// `and`, `or`, `between`, `satisfies` read as operators; `if/then/else`,
// `for/return`, `some/every` as control flow. `instance of` is two words we
// colour individually.
export const FEEL_KEYWORDS = [
  "if", "then", "else", "for", "in", "return", "some", "every", "satisfies",
  "and", "or", "between", "instance", "of", "function",
];

// The three literal constants. `not` is a builtin function in FEEL, not a
// keyword, so it lives in the builtins list.
export const FEEL_LITERALS = ["true", "false", "null"];

// A curated slice of the FEEL builtin functions (DMN 1.3 + the ones Atlas
// evaluates), each with a call signature and a one-line description shown in the
// completion popup. Grouped by area for readability; order here doesn't matter,
// completions are ranked at query time.
export const FEEL_BUILTINS = [
  // Conversion
  { name: "string", sig: "string(from)", doc: "Convert a value to a string." },
  { name: "number", sig: "number(from)", doc: "Parse a string into a number." },
  { name: "date", sig: "date(from)", doc: "A date, e.g. date(\"2020-04-06\")." },
  { name: "time", sig: "time(from)", doc: "A time value." },
  { name: "date and time", sig: "date and time(from)", doc: "A date-time value." },
  { name: "duration", sig: "duration(from)", doc: "A duration, e.g. duration(\"PT2H\")." },
  // Boolean / null
  { name: "not", sig: "not(negand)", doc: "Logical negation of a boolean." },
  { name: "is defined", sig: "is defined(value)", doc: "True unless the value is null / missing." },
  { name: "get or else", sig: "get or else(value, default)", doc: "The value, or the default when it is null." },
  // String
  { name: "substring", sig: "substring(string, start, length?)", doc: "Portion of a string (1-based)." },
  { name: "string length", sig: "string length(string)", doc: "Number of characters." },
  { name: "upper case", sig: "upper case(string)", doc: "Uppercase the string." },
  { name: "lower case", sig: "lower case(string)", doc: "Lowercase the string." },
  { name: "substring before", sig: "substring before(string, match)", doc: "Part before the first match." },
  { name: "substring after", sig: "substring after(string, match)", doc: "Part after the first match." },
  { name: "contains", sig: "contains(string, match)", doc: "True if the string contains match." },
  { name: "starts with", sig: "starts with(string, match)", doc: "True if the string starts with match." },
  { name: "ends with", sig: "ends with(string, match)", doc: "True if the string ends with match." },
  { name: "matches", sig: "matches(input, pattern)", doc: "True if input matches the regex pattern." },
  { name: "replace", sig: "replace(input, pattern, replacement)", doc: "Regex replace." },
  { name: "split", sig: "split(string, delimiter)", doc: "Split into a list of strings." },
  { name: "string join", sig: "string join(list, delimiter)", doc: "Join a list of strings." },
  { name: "trim", sig: "trim(string)", doc: "Remove leading/trailing whitespace." },
  // List
  { name: "list contains", sig: "list contains(list, element)", doc: "True if the list contains the element." },
  { name: "count", sig: "count(list)", doc: "Number of elements." },
  { name: "min", sig: "min(list)", doc: "Smallest element." },
  { name: "max", sig: "max(list)", doc: "Largest element." },
  { name: "sum", sig: "sum(list)", doc: "Sum of the numbers." },
  { name: "mean", sig: "mean(list)", doc: "Arithmetic mean." },
  { name: "median", sig: "median(list)", doc: "Median value." },
  { name: "all", sig: "all(list)", doc: "True if every element is true." },
  { name: "any", sig: "any(list)", doc: "True if any element is true." },
  { name: "sublist", sig: "sublist(list, start, length?)", doc: "Slice of a list (1-based)." },
  { name: "append", sig: "append(list, items...)", doc: "List with items added at the end." },
  { name: "concatenate", sig: "concatenate(lists...)", doc: "Join several lists into one." },
  { name: "distinct values", sig: "distinct values(list)", doc: "Duplicates removed." },
  { name: "flatten", sig: "flatten(list)", doc: "Flatten nested lists." },
  { name: "sort", sig: "sort(list, precedes)", doc: "Sort with a comparator function." },
  { name: "index of", sig: "index of(list, match)", doc: "Positions of match in the list." },
  // Numeric
  { name: "abs", sig: "abs(number)", doc: "Absolute value." },
  { name: "ceiling", sig: "ceiling(number)", doc: "Round up to an integer." },
  { name: "floor", sig: "floor(number)", doc: "Round down to an integer." },
  { name: "round up", sig: "round up(number, scale)", doc: "Round away from zero." },
  { name: "round down", sig: "round down(number, scale)", doc: "Round toward zero." },
  { name: "modulo", sig: "modulo(dividend, divisor)", doc: "Remainder of division." },
  { name: "sqrt", sig: "sqrt(number)", doc: "Square root." },
  { name: "log", sig: "log(number)", doc: "Natural logarithm." },
  { name: "exp", sig: "exp(number)", doc: "e raised to the number." },
  { name: "even", sig: "even(number)", doc: "True if the number is even." },
  { name: "odd", sig: "odd(number)", doc: "True if the number is odd." },
  // Temporal
  { name: "now", sig: "now()", doc: "The current date and time." },
  { name: "today", sig: "today()", doc: "The current date." },
  { name: "day of week", sig: "day of week(date)", doc: "Weekday name of the date." },
  { name: "month of year", sig: "month of year(date)", doc: "Month name of the date." },
];

const KEYWORD_SET = new Set(FEEL_KEYWORDS);
const LITERAL_SET = new Set(FEEL_LITERALS);
const BUILTIN_BY_NAME = new Map(FEEL_BUILTINS.map((b) => [b.name, b]));

// A sticky regex matching any builtin name, longest first so multi-word names
// (`string length`) win over a shorter prefix (`string`).
const BUILTIN_RE = new RegExp(
  "(?:" +
    FEEL_BUILTINS.map((b) => b.name)
      .sort((a, b) => b.length - a.length)
      .map((n) => n.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/ /g, " +"))
      .join("|") +
    ")",
  "y",
);

const isWordChar = (ch) => ch !== undefined && /[A-Za-z0-9_]/.test(ch);

// tokenize splits a FEEL expression into a flat list of { type, value } spans
// covering the whole input (whitespace included), so joining the values
// reproduces the source exactly. Types: comment, string, number, keyword,
// literal, builtin, name, punct, ws.
export function tokenize(src) {
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

    // Block and line comments (FEEL borrows C-style comments).
    if (ch === "/" && src[i + 1] === "*") {
      const end = src.indexOf("*/", i + 2);
      const j = end === -1 ? n : end + 2;
      push("comment", src.slice(i, j));
      i = j;
      continue;
    }
    if (ch === "/" && src[i + 1] === "/") {
      let j = i + 2;
      while (j < n && src[j] !== "\n") j++;
      push("comment", src.slice(i, j));
      i = j;
      continue;
    }

    // Strings. FEEL strings are double-quoted with backslash escapes.
    if (ch === '"') {
      let j = i + 1;
      while (j < n) {
        if (src[j] === "\\") { j += 2; continue; }
        if (src[j] === '"') { j++; break; }
        j++;
      }
      push("string", src.slice(i, j));
      i = j;
      continue;
    }

    // Numbers.
    if (/[0-9]/.test(ch) || (ch === "." && /[0-9]/.test(src[i + 1] || ""))) {
      let j = i;
      while (j < n && /[0-9]/.test(src[j])) j++;
      if (src[j] === ".") { j++; while (j < n && /[0-9]/.test(src[j])) j++; }
      push("number", src.slice(i, j));
      i = j;
      continue;
    }

    // Identifiers, keywords, literals and (possibly multi-word) builtins.
    if (/[A-Za-z_]/.test(ch)) {
      // Try to consume a builtin name first — it may span several words.
      BUILTIN_RE.lastIndex = i;
      const bm = BUILTIN_RE.exec(src);
      if (bm && bm.index === i && !isWordChar(src[i + bm[0].length])) {
        // Normalise the matched (possibly multi-space) text to the canonical
        // key so callers can look up the builtin's signature.
        const canonical = bm[0].replace(/ +/g, " ");
        push("builtin", bm[0]);
        // Attach the canonical name for downstream tooling via a side channel.
        out[out.length - 1].name = canonical;
        i = i + bm[0].length;
        continue;
      }
      let j = i + 1;
      while (j < n && isWordChar(src[j])) j++;
      const word = src.slice(i, j);
      if (KEYWORD_SET.has(word)) push("keyword", word);
      else if (LITERAL_SET.has(word)) push("literal", word);
      else push("name", word);
      i = j;
      continue;
    }

    // Everything else — operators, brackets, punctuation — one char at a time.
    push("punct", ch);
    i++;
  }
  return out;
}

const escapeHTML = (s) => s.replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));

// highlight renders a FEEL expression as an HTML string of <span> tokens for the
// backdrop <pre>. `variables` (a Set of known names) get their own colour so a
// referenced process variable stands out from an unbound identifier.
export function highlight(src, variables) {
  const vars = variables instanceof Set ? variables : new Set(variables || []);
  let html = "";
  for (const tok of tokenize(src)) {
    const text = escapeHTML(tok.value);
    if (tok.type === "ws") { html += text; continue; }
    // A builtin carries its canonical name (multi-word names may span spaces) so
    // a hover handler can look up its signature — see attachFeelEditor's tooltip.
    if (tok.type === "builtin" && tok.name) {
      html += `<span class="tok-builtin" data-fn="${escapeHTML(tok.name)}">${text}</span>`;
      continue;
    }
    let cls = tok.type;
    if (tok.type === "name" && vars.has(tok.value)) cls = "variable";
    html += `<span class="tok-${cls}">${text}</span>`;
  }
  return html;
}

// ---------- Completion ----------

// completionsFor ranks the completion items available for a typed prefix. It
// merges the language's keywords, literals and builtins with the caller-provided
// context variables, then filters/ranks by how the prefix matches the label
// (exact prefix beats a word-start beats a substring). An empty prefix returns
// everything (used for an explicit trigger).
export function completionsFor(prefix, variables) {
  const p = (prefix || "").toLowerCase();
  const items = [];

  for (const name of variables || []) {
    items.push({ label: name, kind: "variable", insert: name, detail: "variable" });
  }
  for (const b of FEEL_BUILTINS) {
    items.push({ label: b.name, kind: "function", insert: b.name, detail: b.sig, doc: b.doc });
  }
  for (const k of FEEL_KEYWORDS) {
    items.push({ label: k, kind: "keyword", insert: k, detail: "keyword" });
  }
  for (const l of FEEL_LITERALS) {
    items.push({ label: l, kind: "literal", insert: l, detail: "literal" });
  }

  const scored = [];
  for (const it of items) {
    const label = it.label.toLowerCase();
    let score;
    if (p === "") score = 0;
    else if (label === p) score = 4;
    else if (label.startsWith(p)) score = 3;
    else if (label.split(/\s+/).some((w) => w.startsWith(p))) score = 2;
    else if (label.includes(p)) score = 1;
    else continue;
    scored.push({ it, score });
  }
  // Highest score first, then variables/keywords ahead of the long builtin list,
  // then alphabetical — a stable, predictable ordering.
  const kindRank = { variable: 0, keyword: 1, literal: 1, function: 2 };
  scored.sort((a, b) =>
    b.score - a.score ||
    (kindRank[a.it.kind] - kindRank[b.it.kind]) ||
    a.it.label.localeCompare(b.it.label));
  return scored.map((s) => s.it);
}

// ---------- Widget ----------

// The identifier word immediately before the caret, used as the completion
// prefix. Only a single word (no spaces) — enough to surface `string length`
// from typing `string`, without the ambiguity of consuming trailing words.
function prefixBefore(text) {
  const m = /[A-Za-z_][A-Za-z0-9_]*$/.exec(text);
  return m ? m[0] : "";
}

// replaceStart finds where the text a completion should overwrite begins. A
// completion whose label is several words (`string length`) may already be
// partly typed (`string len`); we replace that whole run, not just the last
// word, so accepting doesn't duplicate the earlier words. Returns the index in
// `before` at which to start the replacement (defaults to the caret when nothing
// of the label is already present). Exported for testing.
export function replaceStart(before, label) {
  const lower = label.toLowerCase();
  const max = Math.min(before.length, label.length);
  for (let len = max; len > 0; len--) {
    const suffix = before.slice(before.length - len);
    if (!lower.startsWith(suffix.toLowerCase())) continue;
    // The run must begin at a token boundary, so we never cut into an unrelated
    // identifier that merely happens to end with the same letters.
    const prev = before[before.length - len - 1];
    if (prev === undefined || !/[A-Za-z0-9_]/.test(prev)) return before.length - len;
  }
  return before.length;
}

const KIND_LABEL = { variable: "var", function: "fn", keyword: "kw", literal: "lit" };

// attachFeelEditor upgrades an existing <textarea> into a FEEL editor in place.
// It returns a handle with:
//   destroy()          — remove the editor chrome, leaving the bare textarea.
//   setVariables(list) — update the set of known variables for colour/completion.
// opts.variables is the initial list of in-scope variable names.
export function attachFeelEditor(textarea, opts = {}) {
  if (!textarea || textarea.dataset.feelOn === "1") return null;
  textarea.dataset.feelOn = "1";

  let variables = new Set(opts.variables || []);

  // Build the chrome around the textarea: a wrapper, a highlight backdrop, a
  // hidden mirror used to locate the caret, and a completion popup.
  const wrap = document.createElement("div");
  wrap.className = "feel-editor";
  textarea.parentNode.insertBefore(wrap, textarea);

  const pre = document.createElement("pre");
  pre.className = "feel-highlight";
  pre.setAttribute("aria-hidden", "true");
  const code = document.createElement("code");
  pre.appendChild(code);

  const mirror = document.createElement("div");
  mirror.className = "feel-highlight feel-mirror";
  mirror.setAttribute("aria-hidden", "true");

  const pop = document.createElement("div");
  pop.className = "feel-pop";
  pop.setAttribute("role", "listbox");
  pop.hidden = true;

  wrap.appendChild(pre);
  wrap.appendChild(mirror);
  wrap.appendChild(textarea);
  wrap.appendChild(pop);
  textarea.classList.add("feel-input");
  textarea.setAttribute("spellcheck", "false");
  textarea.setAttribute("autocapitalize", "off");
  textarea.setAttribute("autocomplete", "off");

  let items = [];   // current completion items
  let active = -1;  // highlighted item index, or -1

  function renderHighlight() {
    // A trailing newline is not rendered by <pre> unless followed by content;
    // append a zero-width space so the last line's height is preserved and the
    // caret stays aligned.
    code.innerHTML = highlight(textarea.value, variables) + "\u200b";
    pre.scrollTop = textarea.scrollTop;
    pre.scrollLeft = textarea.scrollLeft;
  }

  // caretRect returns the caret's {top,left} within the wrapper, so the popup can
  // be anchored just below it. The mirror shares the textarea's box metrics (same
  // CSS class + copied width), so measuring a marker in it matches the textarea.
  function caretRect() {
    mirror.style.width = textarea.clientWidth + "px";
    const before = textarea.value.slice(0, textarea.selectionStart);
    mirror.textContent = before;
    const marker = document.createElement("span");
    marker.textContent = "\u200b";
    mirror.appendChild(marker);
    const top = marker.offsetTop - textarea.scrollTop;
    const left = marker.offsetLeft - textarea.scrollLeft;
    const lh = marker.offsetHeight || 18;
    mirror.textContent = "";
    return { top, left, lineHeight: lh };
  }

  function closePopup() {
    pop.hidden = true;
    pop.innerHTML = "";
    items = [];
    active = -1;
  }

  function renderPopup() {
    if (!items.length) { closePopup(); return; }
    pop.innerHTML = items
      .map((it, idx) => `
        <div class="feel-opt${idx === active ? " active" : ""}" role="option" data-idx="${idx}"
             aria-selected="${idx === active}">
          <span class="feel-opt-kind k-${it.kind}">${KIND_LABEL[it.kind] || ""}</span>
          <span class="feel-opt-label">${escapeHTML(it.label)}</span>
          <span class="feel-opt-detail">${escapeHTML(it.detail || "")}</span>
        </div>`)
      .join("");
    const r = caretRect();
    // Keep the popup inside the wrapper horizontally.
    const maxLeft = Math.max(0, wrap.clientWidth - 220);
    pop.style.left = Math.min(r.left, maxLeft) + "px";
    pop.style.top = r.top + r.lineHeight + 2 + "px";
    pop.hidden = false;
  }

  function openCompletion(explicit) {
    const prefix = prefixBefore(textarea.value.slice(0, textarea.selectionStart));
    if (!explicit && prefix === "") { closePopup(); return; }
    items = completionsFor(prefix, [...variables]).slice(0, 40);
    active = items.length ? 0 : -1;
    renderPopup();
  }

  function accept(idx) {
    const it = items[idx];
    if (!it) return;
    const caret = textarea.selectionStart;
    const before = textarea.value.slice(0, caret);
    const start = replaceStart(before, it.label);
    let text = it.insert;
    let caretDelta = text.length; // caret lands after the inserted text by default
    if (it.kind === "function") {
      // Insert the call parentheses and drop the caret between them, ready for
      // arguments. `now()`/`today()` take none, so sit just inside — harmless.
      text = it.insert + "()";
      caretDelta = it.insert.length + 1;
    }
    textarea.setRangeText(text, start, caret, "end");
    const pos = start + caretDelta;
    textarea.setSelectionRange(pos, pos);
    closePopup();
    renderHighlight();
    textarea.focus();
    // The value changed programmatically; let listeners (and the highlight) know.
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
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

  // Auto-close brackets and quotes, and let typing the matching closer skip over
  // an auto-inserted one — the small conveniences that make a field feel like an
  // editor. Returns true when it has handled the key.
  const OPENERS = { "(": ")", "[": "]", "{": "}", '"': '"' };
  const CLOSERS = new Set([")", "]", "}", '"']);
  function handleBracket(e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return false;
    const { selectionStart: s, selectionEnd: eSel, value } = textarea;
    // Skip over an auto-inserted closer when the user types it explicitly. This
    // must come first: a quote is both an opener and a closer, so typing the
    // closing quote of an auto-closed pair should step over it, not open a new
    // pair. (Only with an empty selection — a selection means "wrap this".)
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

  function afterEdit() {
    renderHighlight();
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  }

  // ---- live validation ----
  // opts.validate(expr) -> Promise<{ok, error}> compiles the expression against
  // the real engine (the server's FEEL compiler), so a syntax/type error shows
  // in the field as you type instead of only at deploy. Debounced, and guarded
  // by a sequence number so a slow response can't overwrite a newer edit.
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
    wrap.after(statusEl);
  }
  function showValid() {
    wrap.classList.remove("invalid");
    if (statusEl) { statusEl.hidden = true; statusEl.textContent = ""; }
  }
  function showInvalid(msg) {
    wrap.classList.add("invalid");
    if (statusEl) { statusEl.hidden = false; statusEl.textContent = msg; }
  }
  async function runValidate() {
    if (!validate) return;
    const src = textarea.value;
    const seq = ++validateSeq;
    if (src.trim() === "") { showValid(); return; } // empty field = no expression
    let res;
    try { res = await validate(src); }
    catch { return; } // transient/network: don't flag a false error
    if (destroyed || seq !== validateSeq) return; // superseded by a newer edit
    if (res && res.ok === false) showInvalid(res.error || "invalid FEEL expression");
    else showValid();
  }
  function scheduleValidate() {
    if (!validate) return;
    clearTimeout(validateTimer);
    validateTimer = setTimeout(runValidate, 400);
  }

  // ---- builtin signature tooltips ----
  // Hovering a builtin function name shows its signature and one-line doc. The
  // highlighted spans sit *behind* the transparent textarea, so a plain :hover
  // can't reach them; instead we hit-test with elementsFromPoint (which sees
  // through the top textarea) and read the builtin's canonical name from the
  // span's data-fn. The builtin spans opt back into hit-testing via
  // pointer-events:auto (see app.css).
  const tip = document.createElement("div");
  tip.className = "feel-tip";
  tip.hidden = true;
  wrap.appendChild(tip);
  let tipFn = null;
  let hoverRaf = 0;

  function hideTip() { tip.hidden = true; tipFn = null; }

  function updateTip(x, y) {
    const span = document.elementsFromPoint(x, y)
      .find((el) => el.dataset && el.dataset.fn && el.classList.contains("tok-builtin"));
    const b = span ? BUILTIN_BY_NAME.get(span.dataset.fn) : null;
    if (!b) { hideTip(); return; }
    if (span.dataset.fn !== tipFn) {
      tip.innerHTML = `<div class="feel-tip-sig">${escapeHTML(b.sig)}</div><div class="feel-tip-doc">${escapeHTML(b.doc)}</div>`;
      tipFn = span.dataset.fn;
    }
    const r = span.getBoundingClientRect();
    const wr = wrap.getBoundingClientRect();
    const maxLeft = Math.max(0, wrap.clientWidth - 244);
    tip.style.left = Math.min(Math.max(0, r.left - wr.left), maxLeft) + "px";
    tip.style.top = (r.bottom - wr.top + 4) + "px";
    tip.hidden = false;
  }

  const onHover = (e) => {
    if (hoverRaf) return; // coalesce mousemove bursts to one hit-test per frame
    const x = e.clientX, y = e.clientY;
    hoverRaf = requestAnimationFrame(() => { hoverRaf = 0; updateTip(x, y); });
  };

  // ---- event wiring ----
  const onInput = () => { renderHighlight(); openCompletion(false); scheduleValidate(); hideTip(); };
  const onScroll = () => { pre.scrollTop = textarea.scrollTop; pre.scrollLeft = textarea.scrollLeft; };
  const onBlur = () => { setTimeout(closePopup, 120); }; // allow click-to-accept

  function onKeydown(e) {
    if (!pop.hidden) {
      if (e.key === "ArrowDown") { e.preventDefault(); moveActive(1); return; }
      if (e.key === "ArrowUp") { e.preventDefault(); moveActive(-1); return; }
      if (e.key === "Enter" || e.key === "Tab") {
        if (active >= 0) { e.preventDefault(); accept(active); return; }
      }
      if (e.key === "Escape") { e.preventDefault(); closePopup(); return; }
    }
    // Ctrl/Cmd-Space explicitly requests completion (even with no prefix).
    if ((e.ctrlKey || e.metaKey) && e.key === " ") { e.preventDefault(); openCompletion(true); return; }
    if (handleBracket(e)) return;
  }

  // Clicking a completion accepts it. mousedown (not click) so it fires before
  // the textarea's blur closes the popup.
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

  renderHighlight();
  runValidate(); // flag a pre-existing invalid expression immediately

  return {
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
      textarea.classList.remove("feel-input");
      delete textarea.dataset.feelOn;
      if (statusEl) statusEl.remove();
      wrap.parentNode.insertBefore(textarea, wrap);
      wrap.remove();
    },
    setVariables(list) {
      variables = new Set(list || []);
      renderHighlight();
    },
  };
}
