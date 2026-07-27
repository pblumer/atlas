// The FEEL language for the Modeler's expression fields, plugged into the shared
// buildless code editor (code-editor.js).
//
// The Implement tab exposes several FEEL fields (a script task's expression, a
// gateway branch's condition, a message correlation key, and — via the fx toggle —
// any expression-capable field). Plain <textarea>s make those feel like note-taking,
// not programming. This module supplies FEEL's syntax colouring and context-aware
// completion; code-editor.js supplies the editing surface. Both honour the buildless,
// self-contained rules of ADR-0012/ADR-0013 — no framework, bundler, or CDN.
//
// The pure pieces (tokenize, highlight, completionsFor, replaceStart) carry no DOM
// state so they can be reasoned about — and, should a JS test runner ever land,
// tested — on their own.

import { attachCodeEditor } from "./code-editor.js";

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
// literal, builtin, name, punct, ws. A builtin span carries its canonical `name`
// (multi-word names may span spaces) and tip:true so the editor shows its
// signature on hover.
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
        // Attach the canonical name and opt into hover tooltips.
        out[out.length - 1].name = canonical;
        out[out.length - 1].tip = true;
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

// highlight renders a FEEL expression as an HTML string of <span> tokens. Kept as a
// pure export (the editor renders via code-editor.js's shared highlighter now, but
// this remains the standalone, testable reference). `variables` (a Set of known
// names) get their own colour so a referenced process variable stands out.
export function highlight(src, variables) {
  const vars = variables instanceof Set ? variables : new Set(variables || []);
  let html = "";
  for (const tok of tokenize(src)) {
    const text = escapeHTML(tok.value);
    if (tok.type === "ws") { html += text; continue; }
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

// The identifier word immediately before the caret, used as the completion
// prefix. Only a single word (no spaces) — enough to surface `string length`
// from typing `string`, without the ambiguity of consuming trailing words.
export function prefixBefore(text) {
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

// ---------- Language module ----------

// feel is the language module code-editor.js consumes: pure tokenize / completions
// / prefix / replaceStart / tooltip. It adapts the functions above to the module
// shape (function-kind completions insert call parentheses; multi-word builtins use
// the label-aware replaceStart).
export const feel = {
  tokenize,
  prefix(before) {
    const t = prefixBefore(before);
    return { text: t, start: before.length - t.length };
  },
  completions(prefix, ctx) {
    return completionsFor(prefix, ctx.variables).map((it) =>
      it.kind === "function" ? { ...it, call: true } : it);
  },
  replaceStart(before, item) { return replaceStart(before, item.label); },
  tooltip(name) {
    const b = BUILTIN_BY_NAME.get(name);
    return b ? { sig: b.sig, doc: b.doc } : null;
  },
};

// ---------- Widget ----------

// attachFeelEditor upgrades an existing <textarea> into a FEEL editor in place by
// delegating to the shared code editor with the FEEL language module. It returns
// the code-editor handle (destroy / setVariables / setMarkers / focusLine).
//   opts.variables is the initial list of in-scope variable names.
//   opts.validate(expr) -> Promise<{ok, error}> compiles against the real engine.
// FEEL expressions are short, so the field wraps and shows no line-number gutter.
export function attachFeelEditor(textarea, opts = {}) {
  return attachCodeEditor(textarea, {
    lang: feel,
    variables: opts.variables,
    validate: opts.validate,
    wrap: true,
    gutter: false,
  });
}
