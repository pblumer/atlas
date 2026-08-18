// Language registry for the Developer View (ADR-0145).
//
// A field in the Modeler holds one of a handful of *authored* languages: a FEEL
// expression, a job script (PowerShell / Python / JavaScript, ADR-0047), a JSON
// value, a Markdown documentation text, or an HTML fragment. The Developer View
// (dev-view.js) is one modal that has to serve all of them, so everything that
// differs per language is collected here as a single descriptor:
//
//   id         the key a field carries in data-devlang
//   label      what the modal's badge shows
//   module     the code-editor.js language module (tokenize / completions / …)
//   wrap       soft-wrap (prose, short expressions) or horizontal scroll (code)
//   gutter     line numbers
//   format     optional (src) -> string pretty-printer for the Format button
//   overview   a short HTML paragraph for the Help pane's "about" section
//   reference  () -> [{ title, items: [{ name, sig, doc, ex }] }], the function
//              catalogue the Functions and Help panes render
//   snippets   [{ title, doc, code }] ready-to-insert examples
//
// The reference content deliberately lives here rather than in the language
// modules: feel.js / powershell.js carry what the *inline* editor needs to
// highlight and complete (name, signature, one-liner), while the categories and
// worked examples exist only for the Developer View's help pages. Every FEEL
// builtin in feel.js is covered by FEEL_HELP below; one that isn't still shows,
// under "Other", with its signature and one-liner.
//
// Buildless and self-contained like the rest of api/web (ADR-0012, ADR-0013).

import { FEEL_BUILTINS, FEEL_KEYWORDS, FEEL_LITERALS } from "./feel.js";
import { feel } from "./feel.js";
import { powershell, python, javascript, PS_CMDLETS, PS_OPERATORS } from "./powershell.js";
import { tokenizeJSON } from "./json-editor.js";

// ---------- Markdown ----------

// tokenizeMarkdown splits Markdown into spans covering the whole source, so
// joining the values reproduces it exactly (the code-editor backdrop contract).
// It is a line-oriented, deliberately shallow highlighter: block markers (heading,
// list bullet, quote, rule, fence) are recognised per line, inline markers (code,
// bold, italic, link) inside the remaining text. Good enough to make structure
// visible; it is not a Markdown parser.
export function tokenizeMarkdown(src) {
  const out = [];
  const push = (type, value) => { if (value) out.push({ type, value }); };
  const lines = String(src).split("\n");
  let inFence = false;

  lines.forEach((line, i) => {
    if (i > 0) push("ws", "\n");

    const fence = /^\s*(```|~~~)/.exec(line);
    if (fence) {
      inFence = !inFence;
      push("md-code", line);
      return;
    }
    if (inFence) { push("md-code", line); return; }

    if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) { push("md-rule", line); return; }

    let rest = line;
    const heading = /^(\s*#{1,6}\s+)/.exec(rest);
    if (heading) {
      push("md-head", line); // the whole heading line reads as one unit
      return;
    }
    const quote = /^(\s*>+\s?)/.exec(rest);
    if (quote) { push("md-quote", quote[1]); rest = rest.slice(quote[1].length); }
    const bullet = /^(\s*(?:[-*+]|\d+\.)\s+)/.exec(rest);
    if (bullet) { push("md-list", bullet[1]); rest = rest.slice(bullet[1].length); }

    inlineMarkdown(rest, push);
  });

  return out;
}

// inlineMarkdown emits the inline spans of one line's text content.
const MD_INLINE = /(`[^`\n]*`)|(\*\*[^*\n]+\*\*|__[^_\n]+__)|(\*[^*\n]+\*|_[^_\n]+_)|(!?\[[^\]\n]*\]\([^)\n]*\))/;
function inlineMarkdown(text, push) {
  let rest = text;
  while (rest) {
    const m = MD_INLINE.exec(rest);
    if (!m) { push("text", rest); return; }
    push("text", rest.slice(0, m.index));
    if (m[1]) push("md-code", m[1]);
    else if (m[2]) push("md-bold", m[2]);
    else if (m[3]) push("md-em", m[3]);
    else push("md-link", m[4]);
    rest = rest.slice(m.index + m[0].length);
  }
}

// ---------- HTML ----------

// tokenizeHTML splits an HTML fragment into comment / tag / attribute / string /
// entity / text spans. Like the Markdown one it is a highlighter, not a parser:
// embedded <script>/<style> bodies are left as plain text.
export function tokenizeHTML(src) {
  const out = [];
  const push = (type, value) => { if (value) out.push({ type, value }); };
  const s = String(src);
  const n = s.length;
  let i = 0;

  while (i < n) {
    if (s.startsWith("<!--", i)) {
      const end = s.indexOf("-->", i + 4);
      const j = end === -1 ? n : end + 3;
      push("comment", s.slice(i, j));
      i = j;
      continue;
    }
    if (s[i] === "<") {
      const end = s.indexOf(">", i);
      const j = end === -1 ? n : end + 1;
      tagInner(s.slice(i, j), push);
      i = j;
      continue;
    }
    const next = s.indexOf("<", i);
    const j = next === -1 ? n : next;
    entities(s.slice(i, j), push);
    i = j;
  }
  return out;
}

// tagInner emits the spans of a single <...> run: the angle brackets and slash as
// punctuation, the element name, then attribute names and quoted values.
function tagInner(tag, push) {
  // Split off the closer ('>' or '/>') first so it can never be eaten by an
  // unquoted attribute value.
  const closeM = /(\/?>)$/.exec(tag);
  const close = closeM ? closeM[1] : "";
  const body = close ? tag.slice(0, -close.length) : tag;

  const m = /^(<\/?)([A-Za-z][\w:-]*)?/.exec(body);
  const open = (m && m[1]) || "<";
  push("punct", open);
  let i = open.length;
  if (m && m[2]) { push("html-tag", m[2]); i += m[2].length; }

  // Attribute run: names, '=' and values. `expectValue` tracks the token right
  // after an '=' so an unquoted value (id=main) colours as a value, not a name.
  const rest = body.slice(i);
  const attr = /(\s+)|(=)|("[^"]*"|'[^']*')|([A-Za-z_:][\w:.-]*)|([^\s=]+)/g;
  let expectValue = false;
  let a;
  while ((a = attr.exec(rest)) !== null) {
    if (a[1]) { push("ws", a[1]); continue; }
    if (a[2]) { push("punct", a[2]); expectValue = true; continue; }
    if (a[3]) { push("string", a[3]); expectValue = false; continue; }
    push(expectValue ? "string" : a[4] ? "html-attr" : "punct", a[4] || a[5]);
    expectValue = false;
  }
  if (close) push("punct", close);
}

// entities colours &nbsp;-style character references inside text content.
function entities(text, push) {
  const re = /&[#A-Za-z0-9]+;/g;
  let last = 0;
  let m;
  while ((m = re.exec(text)) !== null) {
    push("text", text.slice(last, m.index));
    push("html-entity", m[0]);
    last = m.index + m[0].length;
  }
  push("text", text.slice(last));
}

// A small set of the elements a form/e-mail fragment actually uses, offered as
// completions so a tag is one Ctrl+Space away.
const HTML_TAGS = [
  "a", "b", "br", "button", "div", "em", "form", "h1", "h2", "h3", "hr", "i",
  "img", "input", "label", "li", "ol", "option", "p", "pre", "section", "select",
  "small", "span", "strong", "table", "tbody", "td", "textarea", "th", "thead",
  "tr", "ul",
];

// variableItems turns the caller's in-scope variables into completion items —
// the one thing Markdown and HTML complete on (a documentation text or a form
// fragment names the same variables the rest of the model does).
function variableItems(ctx) {
  return (ctx.variables || [])
    .map((v) => (typeof v === "string" ? { name: v, detail: "variable" } : v))
    .filter((v) => v && v.name)
    .map((v) => ({ label: v.name, kind: "variable", insert: v.name, detail: v.detail || "variable" }));
}

// rank filters and orders completion items by how the typed prefix matches.
function rank(items, prefix) {
  const p = (prefix || "").toLowerCase();
  const scored = [];
  for (const it of items) {
    const label = it.label.toLowerCase();
    let score;
    if (p === "") score = 0;
    else if (label === p) score = 3;
    else if (label.startsWith(p)) score = 2;
    else if (label.includes(p)) score = 1;
    else continue;
    scored.push({ it, score });
  }
  scored.sort((a, b) => b.score - a.score || a.it.label.localeCompare(b.it.label));
  return scored.map((s) => s.it);
}

export const markdown = {
  tokenize: tokenizeMarkdown,
  completions(prefix, ctx) { return rank(variableItems(ctx), prefix); },
  tooltip() { return null; },
};

export const html = {
  tokenize: tokenizeHTML,
  completions(prefix, ctx) {
    return rank([
      ...HTML_TAGS.map((t) => ({ label: t, kind: "type", insert: t, detail: "element" })),
      ...variableItems(ctx),
    ], prefix);
  },
  tooltip() { return null; },
};

// json adapts json-editor.js's tokenizer to the code-editor token contract: a JSON
// key colours like a variable, everything else keeps its name.
const JSON_TOK = { key: "variable", string: "string", number: "number", literal: "literal", punct: "punct", error: "error", ws: "ws" };
export const json = {
  tokenize(src) {
    return tokenizeJSON(src).map((t) => ({ type: JSON_TOK[t.type] || "text", value: t.value }));
  },
  completions(prefix, ctx) { return rank(variableItems(ctx), prefix); },
  tooltip() { return null; },
};

// formatJSON pretty-prints valid JSON and leaves anything else untouched — the
// Format button must never destroy a value it cannot parse.
export function formatJSON(src) {
  try { return JSON.stringify(JSON.parse(src), null, 2); } catch { return src; }
}

// ---------- FEEL help content ----------

// FEEL_HELP adds the two things the Developer View needs on top of what feel.js
// carries for the inline editor: which section of the reference a builtin belongs
// to, and a worked example a reader can copy. Keyed by the exact builtin name.
const FEEL_HELP = {
  "string": ["Conversion", 'string(42) = "42"'],
  "number": ["Conversion", 'number("42.5") = 42.5'],
  "date": ["Conversion", 'date("2026-04-06").year = 2026'],
  "time": ["Conversion", 'time("09:30:00")'],
  "date and time": ["Conversion", 'date and time("2026-04-06T09:30:00")'],
  "duration": ["Conversion", 'duration("PT2H") — two hours'],
  "not": ["Boolean and null", "not(approved)"],
  "is defined": ["Boolean and null", "is defined(customer.email)"],
  "get or else": ["Boolean and null", 'get or else(nickname, "unknown")'],
  "substring": ["String", 'substring("business", 1, 4) = "busi"'],
  "string length": ["String", 'string length("atlas") = 5'],
  "upper case": ["String", 'upper case("atlas") = "ATLAS"'],
  "lower case": ["String", 'lower case("Atlas") = "atlas"'],
  "substring before": ["String", 'substring before("a@b.ch", "@") = "a"'],
  "substring after": ["String", 'substring after("a@b.ch", "@") = "b.ch"'],
  "contains": ["String", 'contains(subject, "invoice")'],
  "starts with": ["String", 'starts with(iban, "CH")'],
  "ends with": ["String", 'ends with(file, ".pdf")'],
  "matches": ["String", 'matches(email, "^[^@]+@[^@]+$")'],
  "replace": ["String", 'replace(iban, "\\s", "")'],
  "split": ["String", 'split("a,b,c", ",") = ["a", "b", "c"]'],
  "string join": ["String", 'string join(tags, ", ")'],
  "trim": ["String", 'trim("  atlas  ") = "atlas"'],
  "list contains": ["List", 'list contains(roles, "approver")'],
  "count": ["List", "count(positions)"],
  "min": ["List", "min([3, 1, 2]) = 1"],
  "max": ["List", "max(offers)"],
  "sum": ["List", "sum(for p in positions return p.amount)"],
  "mean": ["List", "mean(ratings)"],
  "median": ["List", "median(ratings)"],
  "all": ["List", "all(for p in positions return p.amount > 0)"],
  "any": ["List", "any(for p in positions return p.blocked)"],
  "sublist": ["List", "sublist(positions, 1, 10) — the first ten"],
  "append": ["List", 'append(tags, "urgent")'],
  "concatenate": ["List", "concatenate(internal, external)"],
  "distinct values": ["List", "distinct values(countries)"],
  "flatten": ["List", "flatten([[1, 2], [3]]) = [1, 2, 3]"],
  "sort": ["List", "sort(positions, function(a, b) a.amount < b.amount)"],
  "index of": ["List", 'index of(steps, "review")'],
  "abs": ["Number", "abs(-3) = 3"],
  "ceiling": ["Number", "ceiling(1.2) = 2"],
  "floor": ["Number", "floor(1.8) = 1"],
  "round up": ["Number", "round up(1.234, 2) = 1.24"],
  "round down": ["Number", "round down(1.236, 2) = 1.23"],
  "modulo": ["Number", "modulo(12, 5) = 2"],
  "sqrt": ["Number", "sqrt(16) = 4"],
  "log": ["Number", "log(10)"],
  "exp": ["Number", "exp(1)"],
  "even": ["Number", "even(count(positions))"],
  "odd": ["Number", "odd(week)"],
  "now": ["Date and time", "now() — an instant, use for stamps"],
  "today": ["Date and time", 'today() + duration("P3D") — in three days'],
  "day of week": ["Date and time", 'day of week(today()) = "Monday"'],
  "month of year": ["Date and time", 'month of year(today()) = "April"'],
};

// The section order of the FEEL reference — broadest first, so the pane reads
// like a cheat sheet rather than an alphabetical dump.
const FEEL_CATS = ["Conversion", "Boolean and null", "String", "Number", "List", "Date and time", "Other"];

function feelReference() {
  const byCat = new Map(FEEL_CATS.map((c) => [c, []]));
  for (const b of FEEL_BUILTINS) {
    const help = FEEL_HELP[b.name];
    const cat = (help && help[0]) || "Other";
    (byCat.get(cat) || byCat.get("Other")).push({ name: b.name, sig: b.sig, doc: b.doc, ex: help && help[1], call: true });
  }
  const groups = FEEL_CATS
    .filter((c) => (byCat.get(c) || []).length)
    .map((c) => ({ title: c, items: byCat.get(c).sort((a, b) => a.name.localeCompare(b.name)) }));

  groups.push({
    title: "Keywords and operators",
    items: [
      { name: "if / then / else", sig: "if cond then a else b", doc: "Conditional expression — FEEL has no statements, so this is how a branch is written.", ex: 'if amount > 1000 then "manager" else "team"', insert: "if  then  else " },
      { name: "for / return", sig: "for x in list return expr", doc: "Map each element of a list to a new value.", ex: "for p in positions return p.amount * p.quantity", insert: "for  in  return " },
      { name: "some / satisfies", sig: "some x in list satisfies cond", doc: "True if at least one element satisfies the condition.", ex: "some p in positions satisfies p.amount > 1000", insert: "some  in  satisfies " },
      { name: "every / satisfies", sig: "every x in list satisfies cond", doc: "True if every element satisfies the condition.", ex: "every p in positions satisfies p.amount > 0", insert: "every  in  satisfies " },
      { name: "between", sig: "x between a and b", doc: "Inclusive range test.", ex: "amount between 100 and 1000", insert: " between  and " },
      { name: "in", sig: "x in [a, b, c]", doc: "Membership in a list or a range.", ex: 'country in ["CH", "DE", "AT"]', insert: " in " },
      { name: "and / or", sig: "a and b", doc: "Boolean conjunction and disjunction.", ex: "approved and amount <= budget", insert: " and " },
      { name: "instance of", sig: "x instance of type", doc: "Type test.", ex: "amount instance of number", insert: " instance of " },
      { name: "function", sig: "function(a, b) expr", doc: "An inline function value — used by sort().", ex: "function(a, b) a.amount < b.amount", insert: "function() " },
      ...FEEL_LITERALS.map((l) => ({ name: l, sig: l, doc: "Literal constant.", insert: l })),
    ],
  });
  return groups;
}

// ---------- Job-script help content ----------

// The Atlas contract each job-script language honours: how the instance's
// variables arrive, and how a result gets back into the process. This is the
// single thing a developer most needs when they first open a script field.
const SCRIPT_CONTRACT = {
  powershell: {
    read: "Every instance variable is a PowerShell variable: <code>$Vorname</code>.",
    write: "The <b>output stream</b> becomes the result variable — <code>Write-Output</code> or a bare expression.",
    env: "Secrets and configuration arrive as <code>$env:NAME</code>.",
  },
  python: {
    read: "Every instance variable is a module-level name: <code>Vorname</code>.",
    write: "Assign to <code>result</code>; that value becomes the result variable.",
    env: "Secrets and configuration arrive via <code>os.environ[\"NAME\"]</code>.",
  },
  javascript: {
    read: "Every instance variable is a global: <code>Vorname</code>.",
    write: "Assign to <code>result</code>; that value becomes the result variable.",
    env: "Secrets and configuration arrive via <code>process.env.NAME</code>.",
  },
};

function contractGroup(lang) {
  const c = SCRIPT_CONTRACT[lang];
  return {
    title: "Atlas contract",
    items: [
      { name: "Reading variables", sig: "in", doc: c.read.replace(/<[^>]+>/g, ""), ex: null },
      { name: "Returning a result", sig: "out", doc: c.write.replace(/<[^>]+>/g, ""), ex: null },
      { name: "Environment", sig: "env", doc: c.env.replace(/<[^>]+>/g, ""), ex: null },
    ],
  };
}

function psReference() {
  const byCat = new Map();
  for (const c of PS_CMDLETS) {
    const cat = c.name.startsWith("Write-") || c.name === "Out-Null" ? "Output" :
      /Json|RestMethod|WebRequest/.test(c.name) ? "JSON and REST" : "General";
    if (!byCat.has(cat)) byCat.set(cat, []);
    byCat.get(cat).push({ name: c.name, sig: c.sig, doc: c.doc, insert: c.name });
  }
  const groups = [contractGroup("powershell")];
  for (const [title, items] of byCat) groups.push({ title, items: items.sort((a, b) => a.name.localeCompare(b.name)) });
  groups.push({
    title: "Operators",
    items: PS_OPERATORS.map((o) => ({ name: o.name, sig: o.name, doc: o.doc, insert: o.name })),
  });
  return groups;
}

const CLIKE_REF = {
  python: [
    { name: "json.dumps", sig: "json.dumps(obj)", doc: "Serialize an object to a JSON string.", ex: 'result = json.dumps({"ok": True})' },
    { name: "json.loads", sig: "json.loads(text)", doc: "Parse a JSON string into objects.", ex: 'data = json.loads(payload)' },
    { name: "datetime", sig: "datetime.now().isoformat()", doc: "The current timestamp as an ISO-8601 string.", ex: 'result = datetime.now().isoformat()' },
    { name: "f-string", sig: 'f"…{name}…"', doc: "String interpolation over an instance variable.", ex: 'result = f"Hallo {Vorname}"' },
  ],
  javascript: [
    { name: "JSON.stringify", sig: "JSON.stringify(obj)", doc: "Serialize a value to a JSON string.", ex: 'const result = JSON.stringify({ ok: true })' },
    { name: "JSON.parse", sig: "JSON.parse(text)", doc: "Parse a JSON string into a value.", ex: "const data = JSON.parse(payload)" },
    { name: "Array.map", sig: "list.map(fn)", doc: "Map each element of an array.", ex: "const result = positions.map((p) => p.amount)" },
    { name: "template literal", sig: "`…${name}…`", doc: "String interpolation over an instance variable.", ex: "const result = `Hallo ${Vorname}`" },
  ],
};

function clikeReference(lang) {
  return [
    contractGroup(lang),
    { title: "Common calls", items: CLIKE_REF[lang] },
  ];
}

// ---------- Snippets ----------

const SNIPPETS = {
  feel: [
    { title: "Conditional", doc: "Pick a value by a threshold.", code: 'if amount > 1000 then "manager" else "team"' },
    { title: "Sum over a list", doc: "Total a list of line items.", code: "sum(for p in positions return p.amount * p.quantity)" },
    { title: "Filter a list", doc: "Keep only the elements matching a condition.", code: "positions[amount > 100]" },
    { title: "Safe default", doc: "Fall back when a variable is missing.", code: 'get or else(customer.email, "no-reply@example.com")' },
    { title: "Build an object", doc: "A record for an output mapping.", code: '{\n  id: orderId,\n  total: sum(for p in positions return p.amount),\n  approvedAt: string(now())\n}' },
    { title: "Date arithmetic", doc: "A due date three working-ish days out.", code: 'today() + duration("P3D")' },
  ],
  powershell: [
    { title: "Return a string", doc: "The output stream becomes the result variable.", code: 'Write-Output "Hallo $Vorname"' },
    { title: "Return an object", doc: "A hashtable becomes a JSON object variable.", code: '[pscustomobject]@{\n  Name  = $Vorname\n  Total = $Betrag * 1.081\n}' },
    { title: "Call a REST API", doc: "GET a JSON endpoint and return the parsed body.", code: '$r = Invoke-RestMethod -Uri "https://api.example.com/orders/$OrderId" -Method Get\nWrite-Output $r' },
    { title: "Guard a missing variable", doc: "Fail the job with a clear message.", code: 'if (-not $OrderId) { throw "OrderId is missing" }\nWrite-Output $OrderId' },
  ],
  python: [
    { title: "Return a string", doc: "Assign to result.", code: 'result = f"Hallo {Vorname}"' },
    { title: "Return an object", doc: "A dict becomes a JSON object variable.", code: 'result = {\n    "name": Vorname,\n    "total": Betrag * 1.081,\n}' },
    { title: "Work a list", doc: "Total the line items.", code: 'result = sum(p["amount"] for p in positions)' },
    { title: "Guard a missing variable", doc: "Fail the job with a clear message.", code: 'if not OrderId:\n    raise ValueError("OrderId is missing")\nresult = OrderId' },
  ],
  javascript: [
    { title: "Return a string", doc: "Assign to result.", code: "const result = `Hallo ${Vorname}`" },
    { title: "Return an object", doc: "An object becomes a JSON object variable.", code: "const result = {\n  name: Vorname,\n  total: Betrag * 1.081,\n}" },
    { title: "Work a list", doc: "Total the line items.", code: "const result = positions.reduce((a, p) => a + p.amount, 0)" },
    { title: "Guard a missing variable", doc: "Fail the job with a clear message.", code: 'if (!OrderId) throw new Error("OrderId is missing")\nconst result = OrderId' },
  ],
  json: [
    { title: "Object", doc: "A nested value.", code: '{\n  "customer": { "name": "acme", "tier": "gold" },\n  "amount": 100\n}' },
    { title: "List of objects", doc: "The shape a multi-instance activity iterates.", code: '[\n  { "sku": "A-1", "quantity": 2 },\n  { "sku": "B-7", "quantity": 1 }\n]' },
  ],
  markdown: [
    { title: "Step documentation", doc: "The structure the documentation export renders well.", code: "## Purpose\n\nWhat this step is for.\n\n## When it applies\n\n- Condition one\n- Condition two\n\n## Owner\n\nTeam / role responsible." },
    { title: "Table", doc: "A small reference table.", code: "| Field | Meaning |\n| --- | --- |\n| amount | Gross amount in CHF |" },
    { title: "Link", doc: "Point at a policy or a ticket.", code: "[Policy](https://intranet.example.com/policy)" },
  ],
  html: [
    { title: "Paragraph", doc: "A minimal fragment.", code: "<p>Guten Tag <strong>{{name}}</strong>,</p>" },
    { title: "Table", doc: "A small data table.", code: '<table>\n  <tr><th>Position</th><th>Betrag</th></tr>\n  <tr><td>A-1</td><td>120.00</td></tr>\n</table>' },
    { title: "Button link", doc: "A call to action in an e-mail body.", code: '<a href="https://atlas.example.com/tasks" style="padding:8px 12px;background:#0b5cff;color:#fff;border-radius:6px;text-decoration:none">Aufgabe öffnen</a>' },
  ],
};

// ---------- The registry ----------

const LANGS = {
  feel: {
    id: "feel", label: "FEEL", module: feel, wrap: true, gutter: false,
    overview: "FEEL (Friendly Enough Expression Language) is the expression language the engine evaluates itself — no job worker, no round trip. An expression has no statements: it is one value, built from the instance's variables.",
    reference: feelReference,
  },
  powershell: {
    id: "powershell", label: "PowerShell", module: powershell, wrap: false, gutter: true,
    overview: "The script runs on a PowerShell job worker, off the engine's hot path. Instance variables arrive as <code>$name</code>; whatever the script writes to the output stream becomes the task's result variable.",
    reference: psReference,
  },
  python: {
    id: "python", label: "Python", module: python, wrap: false, gutter: true,
    overview: "The script runs on a Python job worker, off the engine's hot path. Instance variables arrive as plain names; assign to <code>result</code> to write a value back into the process.",
    reference: () => clikeReference("python"),
  },
  javascript: {
    id: "javascript", label: "JavaScript", module: javascript, wrap: false, gutter: true,
    overview: "The script runs on a Node.js job worker, off the engine's hot path. Instance variables arrive as globals; assign to <code>result</code> to write a value back into the process.",
    reference: () => clikeReference("javascript"),
  },
  json: {
    id: "json", label: "JSON", module: json, wrap: false, gutter: true, format: formatJSON,
    overview: "A literal JSON value. Format (<kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>F</kbd>) re-indents it; an unparsable value is left exactly as typed.",
    reference: () => [],
  },
  markdown: {
    id: "markdown", label: "Markdown", module: markdown, wrap: true, gutter: true,
    overview: "Documentation text. It is shown to the assignee of a task, in the instance replay, and in the exported process documentation (ADR-0143), so headings and lists are worth using.",
    reference: () => [],
  },
  html: {
    id: "html", label: "HTML", module: html, wrap: false, gutter: true,
    overview: "An HTML fragment — an e-mail body or a small form. Keep it a fragment (no <code>&lt;html&gt;</code> wrapper) and inline any styling, since the renderer strips stylesheets.",
    reference: () => [],
  },
};

// devLang returns the descriptor for a language id, or null for an unknown one.
// The Developer View treats null as "this field is not a code field".
export function devLang(id) {
  return LANGS[id] || null;
}

// devLangIds lists the registered ids — used by the harness and by tests.
export function devLangIds() {
  return Object.keys(LANGS);
}

// snippetsFor returns the example snippets for a language (never null).
export function snippetsFor(id) {
  return SNIPPETS[id] || [];
}

export { FEEL_KEYWORDS };
